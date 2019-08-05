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

package registry // import "helm.sh/helm/pkg/registry"

import (
	"bytes"
	"encoding/json"
	"fmt"
	orascontent "github.com/deislabs/oras/pkg/content"
	"github.com/opencontainers/go-digest"
	checksum "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"

	"helm.sh/helm/pkg/chart"
	"helm.sh/helm/pkg/chart/loader"
	"helm.sh/helm/pkg/chartutil"
)

var (
	tableHeaders = []string{"name", "version", "digest", "size", "created"}
)

type (
	filesystemCache struct {
		out     io.Writer
		rootDir string
		store   *orascontent.Memorystore
	}
)

func (cache *filesystemCache) LayersToChart(layers []ocispec.Descriptor) (*chart.Chart, error) {
	contentLayer, err := extractLayers(layers)
	if err != nil {
		return nil, err
	}

	// Obtain raw chart content
	_, contentRaw, ok := cache.store.Get(contentLayer)
	if !ok {
		return nil, errors.New("error retrieving chart content layer")
	}

	// Construct chart object from raw content
	ch, err := loader.LoadArchive(bytes.NewBuffer(contentRaw))
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

	destDir := mkdir(filepath.Join(cache.rootDir, "blobs", ".build"))
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
	_, contentLayerPath, err := describeReference(cache.rootDir, ref)
	if err != nil {
		return nil, err
	}

	// add content layer
	contentRaw, err := ioutil.ReadFile(contentLayerPath)
	if err != nil {
		return nil, err
	}
	contentLayer := cache.store.Add("", HelmChartContentLayerMediaType, contentRaw)

	//cache.printChartSummary(contentLayer)
	layers := []ocispec.Descriptor{contentLayer}
	return layers, nil
}

func describeReference(cacheRootDir string, ref *Reference) (string, string, error) {
	return "/tmp/manifest", "/tmp/content", nil
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
	contentPath := digestPath(filepath.Join(cache.rootDir, "blobs"), contentLayer.Digest)
	err = writeFile(contentPath, contentRaw)
	if err != nil {
		return exists, err
	}


	// Save config blob
	_, configRaw, ok := cache.store.Get(config)
	if !ok {
		return exists, errors.New("error retrieving config")
	}
	configPath := digestPath(filepath.Join(cache.rootDir, "blobs"), config.Digest)
	err = writeFile(configPath, configRaw)
	if err != nil {
		return exists, err
	}

	fmt.Fprintf(cache.out, "Reference:        %s:%s\n", ref.Repo, ref.Tag)
	cache.printChartSummary(config)
	fmt.Fprintf(cache.out, "Content Digest:   %s\n", contentLayer.Digest.Hex())
	return exists, nil
}

func (cache *filesystemCache) DeleteReference(ref *Reference) error {
	manifestLayerPath, contentLayerPath, err := describeReference(cache.rootDir, ref)
	if err != nil {
		return err
	}

	// Update index.json
	// TODO

	// Delete manifest layer
	err = os.Remove(contentLayerPath)
	if err != nil {
		return err
	}

	// Delete content layer
	err = os.Remove(manifestLayerPath)
	if err != nil {
		return err
	}

	return nil
}

func (cache *filesystemCache) describeReference(rootDir string, ref *Reference) (string, string, error) {
	return "", "", nil
}

func (cache *filesystemCache) TableRows() ([][]interface{}, error) {
	return getRefsSorted(cache.rootDir)
}

// printChartSummary prints details about a chart layers
func (cache *filesystemCache) printChartSummary(config ocispec.Descriptor) {

	metadata := chart.Metadata{}

	// TODO handle errors here
	_, content, _ := cache.store.Get(config)
	json.Unmarshal(content, &metadata)

	fmt.Fprintf(cache.out, "Chart Name:       %s\n", metadata.Name)
	fmt.Fprintf(cache.out, "Chart Version:    %s\n", metadata.Version)

	// TODO print digest elsewhere?
	fmt.Fprintf(cache.out, "Config Digest:    %s\n", config.Digest.Hex())
}

// fileExists determines if a file exists
func fileExists(path string) bool {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}
	return true
}

// mkdir will create a directory (no error check) and return the path
func mkdir(dir string) string {
	os.MkdirAll(dir, 0755)
	return dir
}

// createSymlink creates a symbolic link, deleting existing one if exists
func createSymlink(src string, dest string) error {
	os.Remove(dest)
	err := os.Symlink(src, dest)
	return err
}

// getSymlinkDestContent returns the file contents of a symlink's destination
func getSymlinkDestContent(linkPath string) ([]byte, error) {
	src, err := os.Readlink(linkPath)
	if err != nil {
		return nil, err
	}
	return ioutil.ReadFile(src)
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

// createChartFile creates a file under "<chartsdir>" dir which is linked to by ref
func createChartFile(chartsRootDir string, name string, version string) (string, error) {
	chartPathDir := filepath.Join(chartsRootDir, name, "versions")
	chartPath := filepath.Join(chartPathDir, version)
	if _, err := os.Stat(chartPath); err != nil && os.IsNotExist(err) {
		os.MkdirAll(chartPathDir, 0755)
		err := ioutil.WriteFile(chartPath, []byte("-"), 0644)
		if err != nil {
			return "", err
		}
	}
	return chartPath, nil
}

// digestPath returns the path to addressable content
func digestPath(rootDir string, digest checksum.Digest) string {
	path := filepath.Join(rootDir, "sha256", digest.Hex())
	return path
}

// writeFile creates a path, ensuring parent directory
func writeFile(path string, c []byte) error {
	os.MkdirAll(filepath.Dir(path), 0755)
	return ioutil.WriteFile(path, c, 0644)
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
func getRefsSorted(cacheRootDir string) ([][]interface{}, error) {
	refsMap := map[string]map[string]string{}

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

	var err error
	err = nil
	return refs, err
}
