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

package registry // import "k8s.io/helm/pkg/registry"

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/deislabs/oras/pkg/content"
	"github.com/deislabs/oras/pkg/oras"
	"github.com/gosuri/uitable"
	checksum "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"

	"k8s.io/helm/pkg/chart"
	"k8s.io/helm/pkg/chartutil"
)

type (
	// Client works with OCI-compliant registries and local Helm chart cache
	Client struct {
		CacheRootDir string
		Out          io.Writer
		Resolver     Resolver
	}
)

// RemoveChart deletes a locally saved chart
func (c *Client) RemoveChart(ref *Reference) error {
	tagDir := filepath.Join(c.CacheRootDir, "refs", ref.Locator, "tags", ref.Object)
	fmt.Fprintf(c.Out, "Deleting %s\n", ref.String())
	err := os.RemoveAll(tagDir)
	return err
}

// ListCharts lists locally stored charts
func (c *Client) ListCharts() error {
	table := uitable.New()
	table.MaxColWidth = 60
	table.AddRow("REF", "NAME", "VERSION", "DIGEST", "SIZE", "CREATED")

	refs, err := getAllChartRefs(filepath.Join(c.CacheRootDir, "refs"))
	if err != nil {
		return err
	}

	for k, ref := range refs {
		table.AddRow(k, ref["name"], ref["version"], ref["digest"], ref["size"], ref["created"])
	}

	_, err = fmt.Fprintln(c.Out, table.String())
	return err
}

// PushChart uploads a chart to a registry
func (c *Client) PushChart(ref *Reference) error {
	memoryStore := content.NewMemoryStore()
	tagDir := filepath.Join(c.CacheRootDir, "refs", ref.Locator, "tags", ref.Object)

	// create meta layer
	metaJsonRaw, err := getSymlinkDestContent(filepath.Join(tagDir, "meta"))
	if err != nil {
		return err
	}
	metaLayer := memoryStore.Add(HelmChartMetaFileName, HelmChartMetaMediaType, metaJsonRaw)

	// create content layer
	contentRaw, err := getSymlinkDestContent(filepath.Join(tagDir, "content"))
	if err != nil {
		return err
	}
	contentLayer := memoryStore.Add(HelmChartContentFileName, HelmChartContentMediaType, contentRaw)

	// set annotations on content layer (chart name and version)
	err = setLayerAnnotationsFromChartLink(contentLayer, filepath.Join(tagDir, "chart"))
	if err != nil {
		return err
	}

	// initiate push to remote
	fmt.Fprintf(c.Out, "Pushing %s\nsha256: %s\n", ref.String(), contentLayer.Digest)
	layers := []ocispec.Descriptor{metaLayer, contentLayer}
	err = oras.Push(context.Background(), c.Resolver, ref.String(), memoryStore, layers)
	return err
}

// PullChart downloads a chart from a registry
func (c *Client) PullChart(ref *Reference) error {
	ctx := context.Background()
	memoryStore := content.NewMemoryStore()

	// Create directory which will contain nothing but symlinks
	tagDir := mkdir(filepath.Join(c.CacheRootDir, "refs", ref.Locator, "tags", ref.Object))

	fmt.Fprintf(c.Out, "Pulling %s\n", ref.String())
	layers, err := oras.Pull(ctx, c.Resolver, ref.String(), memoryStore, KnownMediaTypes()...)
	if err != nil {
		return err
	}

	// Retrieve just the meta and content layers
	metaLayer, contentLayer, err := extractLayers(layers)
	if err != nil {
		return err
	}

	// Extract chart name and version
	name, version, err := extractChartNameVersion(contentLayer)
	if err != nil {
		return err
	}

	// Create chart file
	chartPath, err := createChartFile(filepath.Join(c.CacheRootDir, "charts"), name, version)
	if err != nil {
		return err
	}

	// Create chart symlink
	err = createSymlink(chartPath, filepath.Join(tagDir, "chart"))
	if err != nil {
		return err
	}

	// Save meta blob
	_, metaJsonRaw, ok := memoryStore.Get(metaLayer)
	if !ok {
		return errors.New("Error retrieving meta layer")
	}
	metaPath, err := createDigestFile(filepath.Join(c.CacheRootDir, "blobs", "meta"), metaJsonRaw)
	if err != nil {
		return err
	}

	// Create meta symlink
	err = createSymlink(metaPath, filepath.Join(tagDir, "meta"))
	if err != nil {
		return err
	}

	// Save content blob
	_, contentRaw, ok := memoryStore.Get(contentLayer)
	if !ok {
		return errors.New("Error retrieving content layer")
	}
	contentPath, err := createDigestFile(filepath.Join(c.CacheRootDir, "blobs", "content"), contentRaw)
	if err != nil {
		return err
	}

	// Create content symlink
	err = createSymlink(contentPath, filepath.Join(tagDir, "content"))
	return err
}

// SaveChart stores a copy of chart in local cache
func (c *Client) SaveChart(ch *chart.Chart, ref *Reference) error {

	// Create directory which will contain nothing but symlinks
	tagDir := mkdir(filepath.Join(c.CacheRootDir, "refs", ref.Locator, "tags", ref.Object))

	// extract/separate the name and version from other metadata
	name := ch.Metadata.Name
	version := ch.Metadata.Version
	chartPathDir := filepath.Join(c.CacheRootDir, "charts", name, "versions")
	chartPath := filepath.Join(chartPathDir, version)
	if _, err := os.Stat(chartPath); err != nil && os.IsNotExist(err) {
		os.MkdirAll(chartPathDir, 0755)
		err := ioutil.WriteFile(chartPath, []byte("-"), 0644)
		if err != nil {
			return err
		}
	}

	// Create chart symlink
	err := createSymlink(chartPath, filepath.Join(tagDir, "chart"))
	if err != nil {
		return err
	}

	// Clear name and version from Chart.yaml and convert to json
	ch.Metadata.Name = ""
	ch.Metadata.Version = ""
	metaJsonRaw, err := json.Marshal(ch.Metadata)
	if err != nil {
		return err
	}

	// Save meta blob
	digest := checksum.FromBytes(metaJsonRaw).String()
	fmt.Fprintf(c.Out, "repo: %s\ntag: %s\ndigest: %s\n", ref.Locator, ref.Object, digest)
	digestLeft, digestRight := splitDigest(digest)
	metaPathDir := filepath.Join(c.CacheRootDir, "blobs", "meta", "sha256", digestLeft)
	metaPath := filepath.Join(metaPathDir, digestRight)
	if _, err := os.Stat(metaPath); err != nil && os.IsNotExist(err) {
		os.MkdirAll(metaPathDir, 0755)
		err := ioutil.WriteFile(metaPath, metaJsonRaw, 0644)
		if err != nil {
			return err
		}
	}

	// Create meta symlink
	err = createSymlink(metaPath, filepath.Join(tagDir, "meta"))
	if err != nil {
		return err
	}

	// Save content blob
	ch.Metadata = &chart.Metadata{Name: "-", Version: "-"}
	destDir := mkdir(filepath.Join(c.CacheRootDir, "blobs", ".build"))
	tmpFile, err := chartutil.Save(ch, destDir)
	if err != nil {
		return errors.Wrap(err, "failed to save")
	}
	contentRaw, err := ioutil.ReadFile(tmpFile)
	if err != nil {
		return err
	}
	digest = checksum.FromBytes(contentRaw).String()
	digestLeft, digestRight = splitDigest(digest)
	contentPathDir := filepath.Join(c.CacheRootDir, "blobs", "content", "sha256", digestLeft)
	contentPath := filepath.Join(contentPathDir, digestRight)
	if _, err := os.Stat(contentPath); err != nil && os.IsNotExist(err) {
		os.MkdirAll(contentPathDir, 0755)
		err = os.Rename(tmpFile, contentPath)
		if err != nil {
			return err
		}
	} else {
		os.Remove(tmpFile)
	}

	// Create content symlink
	err = createSymlink(contentPath, filepath.Join(tagDir, "content"))
	return err
}
