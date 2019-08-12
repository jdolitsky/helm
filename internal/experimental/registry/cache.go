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
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"
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

	// CacheRefSummary contains as much info as available describing a chart reference in cache
	// Note: fields here are sorted by the order in which they are set in FetchReference method
	CacheRefSummary struct {
		Name         string
		Repo         string
		Tag          string
		Exists       bool
		Manifest     *ocispec.Manifest
		Config       *ocispec.Descriptor
		ContentLayer *ocispec.Descriptor
		Size         int64
		Digest       digest.Digest
		CreatedAt    time.Time
		Chart        *chart.Chart
		ChartName    string
		ChartVersion string
	}
)

// NewCache returns a new OCI Layout-compliant cache with config
func NewCache(options *CacheOptions) (*Cache, error) {
	cache := Cache{
		debug:   options.Debug,
		out:     options.Out,
		rootDir: options.RootDir,
	}
	return &cache, nil
}

// Init creates files needed necessary for OCI layout store
func (cache *Cache) Init() error {
	if cache.ociStore == nil {
		ociStore, err := orascontent.NewOCIStore(cache.rootDir)
		if err != nil {
			return err
		}
		cache.ociStore = ociStore
		cache.memoryStore = orascontent.NewMemoryStore()
	}
	return nil
}

// FetchReference retrieves a ref from cache
func (cache *Cache) FetchReference(ref *Reference) (*CacheRefSummary, error) {
	if err := cache.Init(); err != nil {
		return nil, err
	}
	r := CacheRefSummary{
		Name: ref.FullName(),
		Repo: ref.Repo,
		Tag:  ref.Tag,
	}
	for _, desc := range cache.ociStore.ListReferences() {
		if desc.Annotations[ocispec.AnnotationRefName] == r.Name {
			r.Exists = true
			manifestBytes, err := cache.fetchBlob(&desc)
			if err != nil {
				return &r, err
			}
			var manifest ocispec.Manifest
			err = json.Unmarshal(manifestBytes, &manifest)
			if err != nil {
				return &r, err
			}
			r.Manifest = &manifest
			r.Config = &manifest.Config
			numLayers := len(manifest.Layers)
			if numLayers != 1 {
				return &r, errors.New(
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
				return &r, errors.New(
					fmt.Sprintf("manifest does not contain a layer with mediatype %s", HelmChartContentLayerMediaType))
			}
			r.ContentLayer = contentLayer
			info, err := cache.ociStore.Info(ctx(cache.out, cache.debug), contentLayer.Digest)
			if err != nil {
				return &r, err
			}
			r.Size = info.Size
			r.Digest = info.Digest
			r.CreatedAt = info.CreatedAt
			contentBytes, err := cache.fetchBlob(contentLayer)
			if err != nil {
				return &r, err
			}
			ch, err := loader.LoadArchive(bytes.NewBuffer(contentBytes))
			if err != nil {
				return &r, err
			}
			r.Chart = ch
			r.ChartName = ch.Metadata.Name
			r.ChartVersion = ch.Metadata.Version
		}
	}
	return &r, nil
}

// DeleteRef deletes a ref from cache
func (cache *Cache) StoreReference(ref *Reference, ch *chart.Chart) (*CacheRefSummary, error) {
	if err := cache.Init(); err != nil {
		return nil, err
	}
	r := CacheRefSummary{
		Name:  ref.FullName(),
		Repo:  ref.Repo,
		Tag:   ref.Tag,
		Chart: ch,
	}
	existing, _ := cache.FetchReference(ref)
	r.Exists = existing.Exists
	config, _, err := cache.saveChartConfig(ch)
	if err != nil {
		return &r, err
	}
	r.Config = config
	contentLayer, _, err := cache.saveChartContentLayer(ch)
	if err != nil {
		return &r, err
	}
	r.ContentLayer = contentLayer
	info, err := cache.ociStore.Info(ctx(cache.out, cache.debug), contentLayer.Digest)
	if err != nil {
		return &r, err
	}
	r.Size = info.Size
	r.Digest = info.Digest
	r.CreatedAt = info.CreatedAt
	manifest, manifestDesc, _, err := cache.saveChartManifest(config, contentLayer)
	if err != nil {
		return &r, err
	}
	r.Manifest = manifest
	cache.ociStore.AddReference(r.Name, *manifestDesc)
	err = cache.ociStore.SaveIndex()
	return &r, err
}

// DeleteRef deletes a ref from cache
// TODO: garbage collection, only manifest removed
func (cache *Cache) DeleteReference(ref *Reference) (*CacheRefSummary, error) {
	if err := cache.Init(); err != nil {
		return nil, err
	}
	r, err := cache.FetchReference(ref)
	if err != nil || !r.Exists {
		return r, err
	}
	cache.ociStore.DeleteReference(r.Name)
	err = cache.ociStore.SaveIndex()
	if err != nil {
		return r, err
	}

	return r, err
}

// DeleteRef deletes a ref from cache
func (cache *Cache) ListReferences() ([]*CacheRefSummary, error) {
	var rr []*CacheRefSummary
	if err := cache.Init(); err != nil {
		return rr, err
	}
	for _, desc := range cache.ociStore.ListReferences() {
		name := desc.Annotations[ocispec.AnnotationRefName]
		if name == "" {
			if cache.debug {
				fmt.Fprintf(cache.out, "warning: found manifest without name: %s", desc.Digest.Hex())
			}
			continue
		}
		ref, err := ParseReference(name)
		if err != nil {
			return rr, err
		}
		r, err := cache.FetchReference(ref)
		if err != nil {
			return rr, err
		}
		rr = append(rr, r)
	}
	return rr, nil
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
func (cache *Cache) saveChartManifest(config *ocispec.Descriptor, contentLayer *ocispec.Descriptor) (*ocispec.Manifest, *ocispec.Descriptor, bool, error) {
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		Config:    *config,
		Layers:    []ocispec.Descriptor{*contentLayer},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return nil, nil, false, err
	}
	manifestExists, err := cache.storeBlob(manifestBytes)
	if err != nil {
		return nil, nil, manifestExists, err
	}
	descriptor := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}
	return &manifest, &descriptor, manifestExists, nil
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
