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
	"bytes"
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
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"

	"k8s.io/helm/pkg/chart"
	"k8s.io/helm/pkg/chart/loader"
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

// PushChart uploads a chart to a registry
func (c *Client) PushChart(ref *Reference) error {
	fmt.Fprintf(c.Out, "Pushing %s\n", ref.String())

	memoryStore := content.NewMemoryStore()
	tagDir := filepath.Join(c.CacheRootDir, "refs", ref.Locator, "tags", ref.Object)

	// add meta layer
	metaJsonRaw, err := getSymlinkDestContent(filepath.Join(tagDir, "meta"))
	if err != nil {
		return err
	}
	metaLayer := memoryStore.Add(HelmChartMetaFileName, HelmChartMetaMediaType, metaJsonRaw)

	// add content layer
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
	layers := []ocispec.Descriptor{metaLayer, contentLayer}
	err = oras.Push(context.Background(), c.Resolver, ref.String(), memoryStore, layers)
	return err
}

// PullChart downloads a chart from a registry
func (c *Client) PullChart(ref *Reference) error {
	fmt.Fprintf(c.Out, "Pulling %s\n", ref.String())

	ctx := context.Background()
	memoryStore := content.NewMemoryStore()

	// Create directory which will contain nothing but symlinks
	tagDir := mkdir(filepath.Join(c.CacheRootDir, "refs", ref.Locator, "tags", ref.Object))

	// initiate pull from remote
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
	name, version, err := extractChartNameVersionFromLayer(contentLayer)
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
	fmt.Fprintf(c.Out, "Saving %s\n", ref.String())

	// Create directory which will contain nothing but symlinks
	tagDir := mkdir(filepath.Join(c.CacheRootDir, "refs", ref.Locator, "tags", ref.Object))

	// extract/separate the name and version from other metadata
	name := ch.Metadata.Name
	version := ch.Metadata.Version

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

	// Clear name and version from Chart.yaml and convert to json
	ch.Metadata.Name = ""
	ch.Metadata.Version = ""
	metaJsonRaw, err := json.Marshal(ch.Metadata)
	if err != nil {
		return err
	}

	// Save meta blob
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
	// TODO: something better than this hack. Currently needed for chartutil.Save()
	ch.Metadata = &chart.Metadata{Name: "-", Version: "-"}
	destDir := mkdir(filepath.Join(c.CacheRootDir, "blobs", ".build"))
	tmpFile, err := chartutil.Save(ch, destDir)
	defer os.Remove(tmpFile)
	if err != nil {
		return errors.Wrap(err, "failed to save")
	}
	contentRaw, err := ioutil.ReadFile(tmpFile)
	if err != nil {
		return err
	}
	contentPath, err := createDigestFile(filepath.Join(c.CacheRootDir, "blobs", "content"), contentRaw)
	if err != nil {
		return err
	}

	// Create content symlink
	err = createSymlink(contentPath, filepath.Join(tagDir, "content"))
	return err
}

// LoadChart loads a chart by reference
func (c *Client) LoadChart(ref *Reference) (*chart.Chart, error) {
	tagDir := filepath.Join(c.CacheRootDir, "refs", ref.Locator, "tags", ref.Object)

	// Get chart name and version
	name, version, err := extractChartNameVersionFromRef(filepath.Join(c.CacheRootDir, "refs"), ref)
	if err != nil {
		return nil, err
	}

	// Obtain raw chart meta content (json)
	metaJsonRaw, err := getSymlinkDestContent(filepath.Join(tagDir, "meta"))
	if err != nil {
		return nil, err
	}

	// Construct chart metadata object
	metadata := chart.Metadata{}
	err = json.Unmarshal(metaJsonRaw, &metadata)
	if err != nil {
		return nil, err
	}
	metadata.Name = name
	metadata.Version = version

	// Obtain raw chart content
	contentRaw, err := getSymlinkDestContent(filepath.Join(tagDir, "content"))
	if err != nil {
		return nil, err
	}

	// Construct chart object and attach metadata
	ch, err := loader.LoadArchive(bytes.NewBuffer(contentRaw))
	if err != nil {
		return nil, err
	}
	ch.Metadata = &metadata

	return ch, nil
}

// RemoveChart deletes a locally saved chart
func (c *Client) RemoveChart(ref *Reference) error {
	fmt.Fprintf(c.Out, "Deleting %s\n", ref.String())

	tagDir := filepath.Join(c.CacheRootDir, "refs", ref.Locator, "tags", ref.Object)
	err := os.RemoveAll(tagDir)
	return err
}

// PrintChartTable prints a list of locally stored charts
func (c *Client) PrintChartTable() error {
	table := uitable.New()
	table.MaxColWidth = 60
	table.AddRow("REF", "NAME", "VERSION", "DIGEST", "SIZE", "CREATED")

	refs, err := getRefsSorted(filepath.Join(c.CacheRootDir, "refs"))
	if err != nil {
		return err
	}

	for _, ref := range refs {
		table.AddRow(ref["ref"], ref["name"], ref["version"], ref["digest"], ref["size"], ref["created"])
	}

	fmt.Fprintln(c.Out, table.String())
	return nil
}
