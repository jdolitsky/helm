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
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	orascontent "github.com/deislabs/oras/pkg/content"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"

	"helm.sh/helm/pkg/chart"
	"helm.sh/helm/pkg/chart/loader"
	"helm.sh/helm/pkg/chartutil"
)

const (
	CacheRootDir = "cache"
)

type (
	// CacheOptions is used to construct a new cache
	CacheOptions struct {
		Debug   bool
		Out     io.Writer
		RootDir string
	}

	// Cache handles local/in-memory storage of Helm charts, compliant with OCI Layout
	Cache struct {
		debug       bool
		out         io.Writer
		rootDir     string
		ociStore    *orascontent.OCIStore
		memoryStore *orascontent.Memorystore
	}
)

// NewCache returns a new OCI Layout-compliant cache with config
func NewCache(options *CacheOptions) (*Cache, error) {
	ociStore, err := orascontent.NewOCIStore(options.RootDir)
	if err != nil {
		return nil, err
	}
	cache := Cache{
		debug:       options.Debug,
		out:         options.Out,
		rootDir:     options.RootDir,
		ociStore:    ociStore,
		memoryStore: orascontent.NewMemoryStore(),
	}
	return &cache, nil
}

// storeChartAtRef saves a chart in cache under ref
func (cache *Cache) storeChartAtRef(ch *chart.Chart, ref string) (*ocispec.Descriptor, bool, error) {
	config, _, err := cache.saveChartConfig(ch)
	if err != nil {
		return nil, false, err
	}
	contentLayer, _, err := cache.saveChartContentLayer(ch)
	if err != nil {
		return nil, false, err
	}
	// TODO: better check for chart existence
	manifest, manifestExists, err := cache.saveChartManifest(config, contentLayer)
	if err != nil {
		return nil, manifestExists, err
	}
	cache.ociStore.AddReference(ref, *manifest)
	err = cache.ociStore.SaveIndex()
	return contentLayer, manifestExists, err
}

// fetchChartByRef returns a chart based on ref
func (cache *Cache) fetchChartByRef(ref string) (*chart.Chart, bool, error) {
	for _, m := range cache.ociStore.ListReferences() {
		if m.Annotations[ocispec.AnnotationRefName] == ref {
			c, err := cache.manifestDescriptorToChart(&m)
			if err != nil {
				return nil, false, err
			}
			return c, true, nil
		}
	}
	return nil, false, nil
}

// removeChartByRef removes a chart from cache based on ref
// TODO: garbage collection, only manifest removed
func (cache *Cache) removeChartByRef(ref string) (bool, error) {
	_, exists, err := cache.fetchChartByRef(ref)
	if err != nil || !exists {
		return exists, err
	}
	cache.ociStore.DeleteReference(ref)
	err = cache.ociStore.SaveIndex()
	return exists, err
}

// loadChartDescriptorsByRef returns config and layers represneting a chart, and loads them into memory store
func (cache *Cache) loadChartDescriptorsByRef(ref string) (*ocispec.Descriptor, []ocispec.Descriptor, bool, error) {
	for _, m := range cache.ociStore.ListReferences() {
		if m.Annotations[ocispec.AnnotationRefName] == ref {
			//config := cache.memoryStore.Add("", HelmChartConfigMediaType, []byte("hi"))
			//contentLayer := cache.memoryStore.Add("", HelmChartContentLayerMediaType, []byte("hi"))
			//return &config, []ocispec.Descriptor{contentLayer}, true, nil
		}
	}
	return nil, nil, false, nil
}

// manifestDescriptorToChart converts a descriptor to Chart
func (cache *Cache) manifestDescriptorToChart(desc *ocispec.Descriptor) (*chart.Chart, error) {
	manifestBytes, err := cache.fetchBlob(desc)
	if err != nil {
		return nil, err
	}
	var manifest ocispec.Manifest
	err = json.Unmarshal(manifestBytes, &manifest)
	if err != nil {
		return nil, err
	}
	numLayers := len(manifest.Layers)
	if numLayers != 1 {
		return nil, errors.New(
			fmt.Sprintf("manifest does not contain exactly 1 layer (total: %d)", numLayers))
	}
	var contentLayer *ocispec.Descriptor
	for _, layer := range manifest.Layers {
		switch layer.MediaType {
		case HelmChartContentLayerMediaType:
			contentLayer = &layer
		}
	}
	if contentLayer.Size == 0 {
		return nil, errors.New(
			fmt.Sprintf("manifest does not contain a layer with mediatype %s", HelmChartContentLayerMediaType))
	}
	contentBytes, err := cache.fetchBlob(contentLayer)
	if err != nil {
		return nil, err
	}
	return loader.LoadArchive(bytes.NewBuffer(contentBytes))
}

// saveChartConfig stores the Chart.yaml as json blob and return descriptor
func (cache *Cache) saveChartConfig(ch *chart.Chart) (*ocispec.Descriptor, bool, error) {
	configBytes, err := json.Marshal(ch.Metadata)
	if err != nil {
		return nil, false, err
	}
	configExists, err := cache.storeBlob(configBytes)
	if err != nil {
		return nil, configExists, err
	}
	descriptor := cache.memoryStore.Add("", HelmChartConfigMediaType, configBytes)
	return &descriptor, configExists, nil
}

// saveChartContentLayer stores the chart as tarball blob and return descriptor
func (cache *Cache) saveChartContentLayer(ch *chart.Chart) (*ocispec.Descriptor, bool, error) {
	destDir := filepath.Join(cache.rootDir, ".build")
	os.MkdirAll(destDir, 0755)
	tmpFile, err := chartutil.Save(ch, destDir)
	defer os.Remove(tmpFile)
	if err != nil {
		return nil, false, errors.Wrap(err, "failed to save")
	}
	contentBytes, err := ioutil.ReadFile(tmpFile)
	if err != nil {
		return nil, false, err
	}
	contentExists, err := cache.storeBlob(contentBytes)
	if err != nil {
		return nil, contentExists, err
	}
	descriptor := cache.memoryStore.Add("", HelmChartContentLayerMediaType, contentBytes)
	return &descriptor, contentExists, nil
}

// saveChartManifest stores the chart manifest as json blob and return descriptor
func (cache *Cache) saveChartManifest(config *ocispec.Descriptor, contentLayer *ocispec.Descriptor) (*ocispec.Descriptor, bool, error) {
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		Config:    *config,
		Layers:    []ocispec.Descriptor{*contentLayer},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return nil, false, err
	}
	manifestExists, err := cache.storeBlob(manifestBytes)
	if err != nil {
		return nil, manifestExists, err
	}
	descriptor := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}
	return &descriptor, manifestExists, nil
}

// storeBlob stores a blob on filesystem
func (cache *Cache) storeBlob(blobBytes []byte) (bool, error) {
	var exists bool
	writer, err := cache.ociStore.Store.Writer(ctx(cache.out, cache.debug),
		content.WithRef(digest.FromBytes(blobBytes).Hex()))
	if err != nil {
		return exists, err
	}
	_, err = writer.Write(blobBytes)
	if err != nil {
		return exists, err
	}
	err = writer.Commit(ctx(cache.out, cache.debug), 0, writer.Digest())
	if err != nil {
		if !errdefs.IsAlreadyExists(err) {
			return exists, err
		}
		exists = true
	}
	err = writer.Close()
	return exists, err
}

// fetchBlob retrieves a blob from filesystem
func (cache *Cache) fetchBlob(desc *ocispec.Descriptor) ([]byte, error) {
	reader, err := cache.ociStore.ReaderAt(ctx(cache.out, cache.debug), *desc)
	if err != nil {
		return nil, err
	}
	bytes := make([]byte, desc.Size)
	_, err = reader.ReadAt(bytes, 0)
	if err != nil {
		return nil, err
	}
	return bytes, nil
}
