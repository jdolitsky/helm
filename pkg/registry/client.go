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
	"strings"
	"time"

	"github.com/deislabs/oras/pkg/content"
	"github.com/deislabs/oras/pkg/oras"
	"github.com/docker/go-units"
	"github.com/gosuri/uitable"
	checksum "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"

	"k8s.io/helm/pkg/chart"
	"k8s.io/helm/pkg/chartutil"
)

const (
	// HelmChartMetaMediaType is the reserved media type for Helm chart metadata
	HelmChartMetaMediaType = "application/vnd.cncf.helm.chart.meta.v1+json"

	// HelmChartPackageMediaType is the reserved media type for Helm chart package content
	HelmChartContentMediaType = "application/vnd.cncf.helm.chart.content.v1+tar"

	// HelmChartMetaFileName is the reserved file name for Helm chart metadata
	HelmChartMetaFileName = "chart-meta.json"

	// HelmChartContentFileName is the reserved file name for Helm chart package content
	HelmChartContentFileName = "chart-content.tgz"

	// HelmChartNameAnnotation is the reserved annotation key for Helm chart name
	HelmChartNameAnnotation = "chart.name"

	// HelmChartVersionAnnotation is the reserved annotation key for Helm chart version
	HelmChartVersionAnnotation = "chart.version"
)

// KnownMediaTypes returns a list of layer mediaTypes that the Helm client knows about
func KnownMediaTypes() []string {
	return []string{
		HelmChartMetaMediaType,
		HelmChartContentMediaType,
	}
}

type (
	// Client works with OCI-compliant registries and local Helm chart cache
	Client struct {
		CacheRootDir string
		Out          io.Writer
		Resolver     Resolver
	}
)

// ListCharts lists locally stored charts
func (c *Client) ListCharts() error {
	table := uitable.New()
	table.MaxColWidth = 60
	table.AddRow("REF", "NAME", "VERSION", "DIGEST", "SIZE", "CREATED")

	// Walk the storage dir, check for symlinks under "refs" dir pointing to valid files in "blobs/"
	refs := map[string]map[string]string{}
	refsDir := filepath.Join(c.CacheRootDir, "refs")
	os.MkdirAll(refsDir, 0755)
	err := filepath.Walk(refsDir, func(path string, fileInfo os.FileInfo, fileError error) error {

		// Check if this file is a symlink
		linkPath, err := os.Readlink(path)
		if err == nil {
			destFileInfo, err := os.Stat(linkPath)
			if err == nil {
				tagDir := filepath.Dir(path)

				// Determine the ref
				ref := fmt.Sprintf("%s:%s", strings.TrimLeft(
					strings.TrimPrefix(filepath.Dir(filepath.Dir(tagDir)), refsDir), "/\\"),
					filepath.Base(tagDir))

				// Init hashmap entry if does not exist
				if _, ok := refs[ref]; !ok {
					refs[ref] = map[string]string{}
				}

				// Add data to entry based on file name (symlink name)
				base := filepath.Base(path)
				switch base {
				case "chart":
					refs[ref]["name"] = filepath.Base(filepath.Dir(filepath.Dir(linkPath)))
					refs[ref]["version"] = destFileInfo.Name()
				case "content":
					shaPrefix := filepath.Base(filepath.Dir(linkPath))
					digest := fmt.Sprintf("%s%s", shaPrefix, destFileInfo.Name())

					// Make sure the filename looks like a sha256 digest (64 chars)
					if len(digest) == 64 {
						refs[ref]["digest"] = digest[:7]
						refs[ref]["size"] = byteCountBinary(destFileInfo.Size())
						refs[ref]["created"] = units.HumanDuration(time.Now().UTC().Sub(destFileInfo.ModTime()))
					}
				}
			}
		}

		return nil
	})

	if err != nil {
		return err
	}

	for ref, d := range refs {
		allKeysFound := true
		for _, v := range []string{"name", "version", "digest", "size", "created"} {
			if _, ok := d[v]; !ok {
				allKeysFound = false
				break
			}
		}
		if !allKeysFound {
			continue
		}
		table.AddRow(ref, d["name"], d["version"], d["digest"], d["size"], d["created"])
	}

	_, err = fmt.Fprintln(c.Out, table.String())
	return err
}

// PullChart downloads a chart from a registry
func (c *Client) PullChart(ref *Reference) error {
	ctx := context.Background()
	memoryStore := content.NewMemoryStore()

	// Create directory which will contain nothing but symlinks
	tagDir := filepath.Join(c.CacheRootDir, "refs", ref.Locator, "tags", ref.Object)
	os.MkdirAll(tagDir, 0755)

	fmt.Fprintf(c.Out, "Pulling %s\n", ref.String())
	layers, err := oras.Pull(ctx, c.Resolver, ref.String(), memoryStore, KnownMediaTypes()...)
	if err != nil {
		return err
	}

	if len(layers) != 2 {
		return errors.New("Manifest does not contain exactly 2 layers")
	}

	var metaLayer, contentLayer ocispec.Descriptor
	for _, layer := range layers {
		switch layer.MediaType {
		case HelmChartMetaMediaType:
			metaLayer = layer
		case HelmChartContentMediaType:
			contentLayer = layer
		}
	}

	if metaLayer.Size == 0 {
		return errors.New("Manifest does not contain a Helm chart meta layer")
	}

	if contentLayer.Size == 0 {
		return errors.New("Manifest does not contain a Helm chart content layer")
	}

	// extract/separate the name and version
	name, ok := contentLayer.Annotations[HelmChartNameAnnotation]
	if !ok {
		return errors.New("Could not find chart name in annotations")
	}
	version, ok := contentLayer.Annotations[HelmChartVersionAnnotation]
	if !ok {
		return errors.New("Could not find chart version in annotations")
	}
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
	chartLinkPath := filepath.Join(tagDir, "chart")
	os.Remove(chartLinkPath)
	err = os.Symlink(chartPath, chartLinkPath)
	if err != nil {
		return err
	}

	// Save meta blob
	_, metaJsonRaw, ok := memoryStore.Get(metaLayer)
	if !ok {
		return errors.New("Error retrieving meta layer")
	}
	digest := checksum.FromBytes(metaJsonRaw).String()
	digestLeft := digest[7:9]
	digestRight := digest[9:71]
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
	metaLinkPath := filepath.Join(tagDir, "meta")
	os.Remove(metaLinkPath)
	err = os.Symlink(metaPath, metaLinkPath)
	if err != nil {
		return err
	}

	// Save content blob
	_, contentRaw, ok := memoryStore.Get(contentLayer)
	if !ok {
		return errors.New("Error retrieving content layer")
	}
	digest = checksum.FromBytes(contentRaw).String()
	digestLeft = digest[7:9]
	digestRight = digest[9:71]
	contentPathDir := filepath.Join(c.CacheRootDir, "blobs", "content", "sha256", digestLeft)
	contentPath := filepath.Join(contentPathDir, digestRight)
	if _, err := os.Stat(contentPath); err != nil && os.IsNotExist(err) {
		os.MkdirAll(contentPathDir, 0755)
		err := ioutil.WriteFile(contentPath, contentRaw, 0644)
		if err != nil {
			return err
		}
	}

	// Create content symlink
	contentLinkPath := filepath.Join(tagDir, "content")
	os.Remove(contentLinkPath)
	err = os.Symlink(contentPath, contentLinkPath)
	if err != nil {
		return err
	}

	return nil
}

// PushChart uploads a chart to a registry
func (c *Client) PushChart(ref *Reference) error {
	ctx := context.Background()
	memoryStore := content.NewMemoryStore()
	tagDir := filepath.Join(c.CacheRootDir, "refs", ref.Locator, "tags", ref.Object)

	// create meta layer
	metaLink := filepath.Join(tagDir, "meta")
	metaPath, err := os.Readlink(metaLink)
	if err != nil {
		return err
	}
	metaContent, err := ioutil.ReadFile(metaPath)
	if err != nil {
		return err
	}
	metaLayer := memoryStore.Add(HelmChartMetaFileName, HelmChartMetaMediaType, metaContent)

	// create content layer
	contentLink := filepath.Join(tagDir, "content")
	contentPath, err := os.Readlink(contentLink)
	if err != nil {
		return err
	}
	chartContent, err := ioutil.ReadFile(contentPath)
	if err != nil {
		return err
	}
	contentLayer := memoryStore.Add(HelmChartContentFileName, HelmChartContentMediaType, chartContent)

	// add chart name and version as annotations
	chartLink := filepath.Join(tagDir, "chart")
	chartPath, err := os.Readlink(chartLink)
	if err != nil {
		return err
	}
	chartName := filepath.Base(filepath.Dir(filepath.Dir(chartPath)))
	chartVersion := filepath.Base(chartPath)
	contentLayer.Annotations[HelmChartNameAnnotation] = chartName
	contentLayer.Annotations[HelmChartVersionAnnotation] = chartVersion

	// do push
	layers := []ocispec.Descriptor{metaLayer, contentLayer}
	fmt.Fprintf(c.Out, "Pushing %s\nsha256: %s\n", ref, contentLayer.Digest)
	return oras.Push(ctx, c.Resolver, ref.String(), memoryStore, layers)
}

// RemoveChart deletes a locally saved chart
func (c *Client) RemoveChart(ref *Reference) error {
	tagDir := filepath.Join(c.CacheRootDir, "refs", ref.Locator, "tags", ref.Object)
	fmt.Fprintf(c.Out, "Deleting %s\n", ref.String())
	return os.RemoveAll(tagDir)
}

// SaveChart stores a copy of chart in local cache
func (c *Client) SaveChart(ch *chart.Chart, ref *Reference) error {

	// Create directory which will contain nothing but symlinks
	tagDir := filepath.Join(c.CacheRootDir, "refs", ref.Locator, "tags", ref.Object)
	os.MkdirAll(tagDir, 0755)

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
	chartLinkPath := filepath.Join(tagDir, "chart")
	os.Remove(chartLinkPath)
	err := os.Symlink(chartPath, chartLinkPath)
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
	digestLeft := digest[7:9]
	digestRight := digest[9:71]
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
	metaLinkPath := filepath.Join(tagDir, "meta")
	os.Remove(metaLinkPath)
	err = os.Symlink(metaPath, metaLinkPath)
	if err != nil {
		return err
	}

	// Save content blob
	ch.Metadata = &chart.Metadata{Name: "-", Version: "-"}
	destDir := filepath.Join(c.CacheRootDir, "blobs", ".build")
	os.MkdirAll(destDir, 0755)
	tmpFile, err := chartutil.Save(ch, destDir)
	if err != nil {
		return errors.Wrap(err, "failed to save")
	}
	contentRaw, err := ioutil.ReadFile(tmpFile)
	if err != nil {
		return err
	}
	digest = checksum.FromBytes(contentRaw).String()
	digestLeft = digest[7:9]
	digestRight = digest[9:71]
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
	contentLinkPath := filepath.Join(tagDir, "content")
	os.Remove(contentLinkPath)
	err = os.Symlink(contentPath, contentLinkPath)
	if err != nil {
		return err
	}

	fmt.Fprintf(c.Out, "repo: %s\ntag: %s\ndigest: %s\n", ref.Locator, ref.Object, digest)
	return nil
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
