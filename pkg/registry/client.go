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
	"fmt"
	"io"
	"io/ioutil"
	"k8s.io/helm/pkg/chart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/go-units"
	"github.com/gosuri/uitable"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/shizhMSFT/oras/pkg/content"
	"github.com/shizhMSFT/oras/pkg/oras"

	"k8s.io/helm/pkg/chartutil"
	"k8s.io/helm/pkg/provenance"
)

const (
	// HelmChartMetaMediaType is the reserved media type for Helm chart metadata
	HelmChartMetaMediaType = "application/vnd.cncf.helm.chart.meta.v1+json"

	// HelmChartPackageMediaType is the reserved media type for Helm chart package content
	HelmChartContentMediaType = "application/vnd.cncf.helm.chart.content.v1+tar"
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

	// Walk the storage dir, check for symlinks under "refs" dir pointing to valid files in "blobs/sha256"
	refsDir := filepath.Join(c.CacheRootDir, "refs")
	os.MkdirAll(refsDir, 0755)
	err := filepath.Walk(refsDir, func(path string, fileInfo os.FileInfo, fileError error) error {

		// Check if this file is a symlink (tag)
		blobPath, err := os.Readlink(path)
		if err == nil {
			blobFileInfo, err := os.Stat(blobPath)
			if err == nil {
				tag := filepath.Base(path)
				repo := strings.TrimRight(strings.TrimSuffix(path, tag), "/\\")

				// Make sure the filename looks like a sha256 digest (64 chars)
				if digest := filepath.Base(blobPath); len(digest) == 64 {

					// Make sure this file is in a valid location
					if blobPath == filepath.Join(c.CacheRootDir, "blobs", "sha256", digest) {
						ref := strings.TrimLeft(strings.TrimPrefix(repo, filepath.Join(c.CacheRootDir, "refs")), "/\\")
						ref = fmt.Sprintf("%s:%s", ref, tag)
						name := filepath.Base(repo)
						created := units.HumanDuration(time.Now().UTC().Sub(blobFileInfo.ModTime()))
						size := byteCountBinary(blobFileInfo.Size())
						table.AddRow(ref, name, tag, digest[:7], size, created)
					}
				}
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(c.Out, table.String())
	return err
}

// PullChart downloads a chart from a registry
func (c *Client) PullChart(ref *Reference) error {
	destDir := filepath.Join(c.CacheRootDir, "blobs", "sha256")
	os.MkdirAll(destDir, 0755)
	ctx := context.Background()
	memoryStore := content.NewMemoryStore()

	fmt.Fprintf(c.Out, "Pulling %s\n", ref.String())
	pullContents, err := oras.Pull(ctx, c.Resolver, ref.String(), memoryStore, KnownMediaTypes()...)
	if err != nil {
		return err
	}

	for _, descriptor := range pullContents {
		digest := descriptor.Digest.Hex()
		fmt.Fprintf(c.Out, "sha256: %s\n", digest)
		_, content, ok := memoryStore.Get(descriptor)
		if !ok {
			return errors.New("error accessing pulled content")
		}

		blobPath := filepath.Join(destDir, digest)
		if _, err := os.Stat(blobPath); err != nil && os.IsNotExist(err) {
			err := ioutil.WriteFile(blobPath, content, 0644)
			if err != nil {
				return err
			}
		}

		tagDir := filepath.Join(c.CacheRootDir, "refs", ref.Locator)
		os.MkdirAll(tagDir, 0755)
		tagPath := filepath.Join(tagDir, ref.Object)
		os.Remove(tagPath)
		err = os.Symlink(blobPath, tagPath)
		if err != nil {
			return err
		}
	}

	return nil
}

// PushChart uploads a chart to a registry
func (c *Client) PushChart(ref *Reference) error {
	blobLink := filepath.Join(c.CacheRootDir, "refs", ref.Locator, ref.Object)
	blobPath, err := os.Readlink(blobLink)
	if err != nil {
		return err
	}

	digest := filepath.Base(blobPath)

	fileContent, err := ioutil.ReadFile(blobPath)
	if err != nil {
		return err
	}

	ctx := context.Background()
	memoryStore := content.NewMemoryStore()

	desc := memoryStore.Add(digest, HelmChartContentMediaType, fileContent)
	pushContents := []ocispec.Descriptor{desc}

	fmt.Fprintf(c.Out, "Pushing %s\nsha256: %s\n", ref, digest)
	return oras.Push(ctx, c.Resolver, ref.String(), memoryStore, pushContents)
}

// RemoveChart deletes a locally saved chart
func (c *Client) RemoveChart(ref *Reference) error {
	blobLink := filepath.Join(c.CacheRootDir, "refs", ref.Locator, ref.Object)
	fmt.Fprintf(c.Out, "Deleting %s\n", ref.String())
	return os.Remove(blobLink)
}

// SaveChart stores a copy of chart in local cache
func (c *Client) SaveChart(ch *chart.Chart, ref *Reference) error {
	destDir := filepath.Join(c.CacheRootDir, "blobs", "sha256")
	os.MkdirAll(destDir, 0755)
	tmpFile, err := chartutil.Save(ch, destDir)
	if err != nil {
		return errors.Wrap(err, "failed to save")
	}

	content, err := ioutil.ReadFile(tmpFile)
	if err != nil {
		return err
	}

	digest, err := provenance.Digest(bytes.NewBuffer(content))
	if err != nil {
		return err
	}

	blobPath := filepath.Join(destDir, digest)

	if _, err := os.Stat(blobPath); err != nil && os.IsNotExist(err) {
		err = os.Rename(tmpFile, blobPath)
		if err != nil {
			return err
		}
	} else {
		os.Remove(tmpFile)
	}

	blobLinkParentDir := filepath.Join(c.CacheRootDir, "refs", ref.Locator)
	os.MkdirAll(blobLinkParentDir, 0755)
	blobLink := filepath.Join(blobLinkParentDir, ref.Object)
	os.Remove(blobLink)
	err = os.Symlink(blobPath, blobLink)
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
