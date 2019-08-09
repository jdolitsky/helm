/*
Copyright The Helm Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package registry // import "helm.sh/helm/internal/experimental/registry"

import (
	"bytes"
	"encoding/json"
	"fmt"
	orascontent "github.com/deislabs/oras/pkg/content"
	"github.com/docker/go-units"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"helm.sh/helm/pkg/chart"
	"helm.sh/helm/pkg/chart/loader"
	"helm.sh/helm/pkg/chartutil"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"time"
)

var (
	tableHeaders = []string{"name", "version", "digest", "size", "created"}
)

type (
	filesystemCache struct {
		out     io.Writer
		rootDir string
		store   *orascontent.Memorystore
		index   *OCIIndex
	}
)

func (cache *filesystemCache) LayersToChart(layers []ocispec.Descriptor) (*chart.Chart, error) {
	contentLayer, err := extractLayers(layers)
	if err != nil {
		return nil, err
	}

	// Construct chart object from raw content
	raw, err := cache.index.FetchBlob(contentLayer.Digest.Hex())
	ch, err := loader.LoadArchive(bytes.NewBuffer(raw))
	if err != nil {
		return nil, err
	}

	return ch, nil
}

func (cache *filesystemCache) ChartToLayers(ch *chart.Chart) (ocispec.Descriptor, []ocispec.Descriptor, error) {
	var config ocispec.Descriptor

	if err := ch.Validate(); err != nil {
		return config, nil, err
	}

	// Set the metadata as config content
	configRaw, err := json.Marshal(ch.Metadata)
	if err != nil {
		return config, nil, errors.Wrap(err, "could not convert metadata to json")
	}

	config = ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.FromBytes(configRaw),
		Size:      int64(len(configRaw)),
	}
	cache.store.Set(config, configRaw)

	destDir := filepath.Join(cache.rootDir, ".build")
	os.MkdirAll(destDir, 0755)
	tmpFile, err := chartutil.Save(ch, destDir)
	defer os.Remove(tmpFile)
	if err != nil {
		return config, nil, errors.Wrap(err, "failed to save")
	}
	contentRaw, err := ioutil.ReadFile(tmpFile)
	if err != nil {
		return config, nil, err
	}

	contentLayer := cache.store.Add("", HelmChartContentLayerMediaType, contentRaw)
	layers := []ocispec.Descriptor{contentLayer}

	return config, layers, nil
}

func (cache *filesystemCache) LoadReference(ref *Reference) ([]ocispec.Descriptor, error) {
	refStr := fmt.Sprintf("%s:%s", ref.Repo, ref.Tag)
	m, exists := cache.index.GetManifestByRef(refStr)
	if !exists {
		return nil, errors.New("ref not found")
	}
	return m.Layers, nil
}

func (cache *filesystemCache) StoreReference(ref *Reference, config ocispec.Descriptor, layers []ocispec.Descriptor) (bool, error) {
	var exists bool

	// Retrieve content layer
	contentLayer, err := extractLayers(layers)
	if err != nil {
		return exists, err
	}

	// Save content blob
	_, contentRaw, ok := cache.store.Get(contentLayer)
	if !ok {
		return exists, errors.New("error retrieving content layer")
	}

	_, err = cache.index.StoreBlob(contentRaw)
	if err != nil {
		return exists, err
	}

	// Save config blob
	_, configRaw, ok := cache.store.Get(config)
	if !ok {
		return exists, errors.New("error retrieving config")
	}
	_, err = cache.index.StoreBlob(configRaw)
	if err != nil {
		return exists, err
	}

	fmt.Fprintf(cache.out, "Reference:        %s:%s\n", ref.Repo, ref.Tag)
	cache.printChartSummary(config)

	fmt.Fprintf(cache.out, "Content Size:     %s\n", byteCountBinary(contentLayer.Size))
	fmt.Fprintf(cache.out, "Content Digest:   %s\n", contentLayer.Digest.Hex())
	fmt.Fprintf(cache.out, "Config Digest:    %s\n", config.Digest.Hex())

	return exists, nil
}

func (cache *filesystemCache) DeleteReference(ref *Reference) error {
	refStr := fmt.Sprintf("%s:%s", ref.Repo, ref.Tag)
	_, _ = cache.index.DeleteManifestByRef(refStr)
	cache.index.Save()
	return nil
	//_, err := cache.index.DeleteBlob(m.Digest.Hex())
	//return err
}

func (cache *filesystemCache) TableRows() ([][]interface{}, error) {
	return cache.getRefsSorted()
}

// printChartSummary prints details about a chart layers
func (cache *filesystemCache) printChartSummary(config ocispec.Descriptor) {

	metadata := chart.Metadata{}

	// TODO handle errors here
	_, content, _ := cache.store.Get(config)
	json.Unmarshal(content, &metadata)

	fmt.Fprintf(cache.out, "Chart Name:       %s\n", metadata.Name)
	fmt.Fprintf(cache.out, "Chart Version:    %s\n", metadata.Version)

}

// extractLayers obtains the content layer from a list of layers
func extractLayers(layers []ocispec.Descriptor) (ocispec.Descriptor, error) {
	var contentLayer ocispec.Descriptor

	if len(layers) != 1 {
		return contentLayer, errors.New("manifest does not contain exactly 1 layer")
	}

	for _, layer := range layers {
		switch layer.MediaType {
		case HelmChartContentLayerMediaType:
			contentLayer = layer
		}
	}

	if contentLayer.Size == 0 {
		return contentLayer, errors.New("manifest does not contain a valid Helm chart content layer")
	}

	return contentLayer, nil
}

// byteCountBinary produces a human-readable file size
func byteCountBinary(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// shortDigest returns first 7 characters of a sha256 digest
func shortDigest(digest string) string {
	if len(digest) == 64 {
		return digest[:7]
	}
	return digest
}

// getRefsSorted returns a map of all refs stored in a cache
func (cache *filesystemCache) getRefsSorted() ([][]interface{}, error) {
	refsMap := map[string]map[string]string{}

	for _, manifest := range cache.index.Manifests {
		if ref, ok := manifest.Annotations[ocispec.AnnotationRefName]; ok {
			manifestRaw, err := cache.index.FetchBlob(manifest.Digest.Hex())
			if err != nil {
				return nil, err
			}

			var manifest ocispec.Manifest
			err = json.Unmarshal(manifestRaw, &manifest)
			if err != nil {
				return nil, err
			}

			configRaw, err := cache.index.FetchBlob(manifest.Config.Digest.Hex())
			if err != nil {
				return nil, err
			}
			var metadata chart.Metadata
			err = json.Unmarshal(configRaw, &metadata)
			if err != nil {
				return nil, err
			}

			refsMap[ref] = map[string]string{}
			refsMap[ref]["name"] = metadata.Name
			refsMap[ref]["version"] = metadata.Version

			contentLayer, err := extractLayers(manifest.Layers)
			if err != nil {
				return nil, err
			}

			refsMap[ref]["digest"] = shortDigest(contentLayer.Digest.Hex())
			refsMap[ref]["size"] = byteCountBinary(contentLayer.Size)

			contentPath, err := cache.index.getBlobPath(contentLayer.Digest.Hex())
			if err != nil {
				return nil, err
			}
			destFileInfo, err := os.Stat(contentPath)
			if err != nil {
				return nil, err
			}

			refsMap[ref]["created"] = units.HumanDuration(time.Now().UTC().Sub(destFileInfo.ModTime()))
		}
	}

	// Filter out any refs that are incomplete (do not have all required fields)
	for k, ref := range refsMap {
		allKeysFound := true
		for _, v := range tableHeaders {
			if _, ok := ref[v]; !ok {
				allKeysFound = false
				break
			}
		}
		if !allKeysFound {
			delete(refsMap, k)
		}
	}

	// Sort and convert to format expected by uitable
	refs := make([][]interface{}, len(refsMap))
	keys := make([]string, 0, len(refsMap))
	for key := range refsMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i, key := range keys {
		refs[i] = make([]interface{}, len(tableHeaders)+1)
		refs[i][0] = key
		ref := refsMap[key]
		for j, k := range tableHeaders {
			refs[i][j+1] = ref[k]
		}
	}

	return refs, nil
}
