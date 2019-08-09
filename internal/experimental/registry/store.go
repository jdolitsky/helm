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
	//"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	//"github.com/opencontainers/go-digest"
	//"github.com/opencontainers/image-spec/specs-go"
	"github.com/docker/go-units"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	//"github.com/pkg/errors"
	"helm.sh/helm/pkg/chart"
	//"helm.sh/helm/pkg/chart/loader"
	//"helm.sh/helm/pkg/chartutil"
	//"io/ioutil"
	//"os"
	//"path/filepath"
)

var (
	tableHeaders = []string{"name", "version", "digest", "size", "created"}
)

type (
	// Authorizer handles registry auth operations
	Store struct {
		//orascontent.OCIStore
		RootDir string
	}
)

func NewStore(rootDir string) (*Store, error) {
	return &Store{RootDir: rootDir}, nil
}

func (store *Store) LoadReference(ref *Reference) ([]ocispec.Descriptor, error) {

}

func (store *Store) GetManifestByRef(ref string) (*ocispec.Manifest, bool) {

}

func (store *Store) StoreBlob(blob []byte) (string, error) {

}

func (store *Store) FetchBlob(digest string) ([]byte, error) {

}

func (store *Store) DeleteBlob(digest string) ([]byte, error) {

}

func (store *Store) StoreReference(ref *Reference, config ocispec.Descriptor, layers []ocispec.Descriptor) (bool, error) {

	//fmt.Fprintf(cache.out, "ref:     %s\n", ref.FullName())
	//fmt.Fprintf(cache.out, "digest:  %s\n", contentLayer.Digest.Hex())
	//fmt.Fprintf(cache.out, "size:    %s\n", byteCountBinary(contentLayer.Size))
	//fmt.Fprintf(cache.out, "name:    %s\n", metadata.Name)
	//fmt.Fprintf(cache.out, "version: %s\n", metadata.Version)

}

func (store *Store) ChartToLayers(ch *chart.Chart) (ocispec.Descriptor, []ocispec.Descriptor, error) {

}

func (store *Store) AddManifest(config ocispec.Descriptor, layers []ocispec.Descriptor, ref string) ([]byte, string, error) {

}

func (store *Store) DeleteReference(ref *Reference) error {

}

func (store *Store) LayersToChart(layers []ocispec.Descriptor) (*chart.Chart, error) {

}

func (store *Store) TableRows() ([][]interface{}, error) {

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
func (store *Store) getRefsSorted() ([][]interface{}, error) {
	refsMap := map[string]map[string]string{}

	for _, manifest := range store.GetAllManifests {
		if ref, ok := manifest.Annotations[ocispec.AnnotationRefName]; ok {
			manifestRaw, err := store.FetchBlob(manifest.Digest.Hex())
			if err != nil {
				return nil, err
			}

			var manifest ocispec.Manifest
			err = json.Unmarshal(manifestRaw, &manifest)
			if err != nil {
				return nil, err
			}

			configRaw, err := store.FetchBlob(manifest.Config.Digest.Hex())
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

			contentPath, err := store.getBlobPath(contentLayer.Digest.Hex())
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
