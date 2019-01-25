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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/containerd/remotes/docker"
	"github.com/docker/go-units"
	"github.com/gosuri/uitable"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/shizhMSFT/oras/pkg/content"
	"github.com/shizhMSFT/oras/pkg/oras"

	"k8s.io/helm/pkg/chart/loader"
	"k8s.io/helm/pkg/chartutil"
	"k8s.io/helm/pkg/provenance"
)

const (
	// HelmChartNameMediaType is the reserved media type for Helm chart name
	HelmChartNameMediaType = "application/vnd.cncf.helm.chart.name.v1.txt"

	// HelmChartVersionMediaType is the reserved media type for Helm chart version
	HelmChartVersionMediaType = "application/vnd.cncf.helm.chart.version.v1.txt"

	// HelmChartPackageMediaType is the reserved media type for Helm chart package content
	HelmChartPackageMediaType = "application/vnd.cncf.helm.chart.content.v1.tar"
)

// ListCharts lists locally stored charts
func ListCharts(out io.Writer, storageRootDir string) error {
	table := uitable.New()
	table.MaxColWidth = 60
	table.AddRow("REF", "NAME", "VERSION", "DIGEST", "SIZE", "CREATED")

	// Walk the storage dir, check for symlinks under "refs" dir pointing to valid files in "blobs/sha256"
	refsDir := filepath.Join(storageRootDir, "refs")
	os.MkdirAll(refsDir, 0755)
	err := filepath.Walk(refsDir, func(path string, fileInfo os.FileInfo, fileError error) error {

		// Check if this file is a symlink (tag)
		blobPath, err := os.Readlink(path)
		if err == nil {
			blobFileInfo, err := os.Stat(blobPath)
			if err == nil {
				tag := filepath.Base(path)
				repo := strings.TrimRight(strings.TrimSuffix(path, tag), "/\\")

				// Make sure the filename looks like a sha256 digest (64 chars
				if digest := filepath.Base(blobPath); len(digest) == 64 {

					// Make sure this file is in a valid location
					if blobPath == filepath.Join(storageRootDir, "blobs", "sha256", digest) {
						ref := strings.TrimLeft(strings.TrimPrefix(repo, filepath.Join(storageRootDir, "refs")), "/\\")
						ref = fmt.Sprintf("%s:%s", ref, tag)
						name := filepath.Base(repo)
						created := units.HumanDuration(time.Now().UTC().Sub(blobFileInfo.ModTime()))
						size := byteCountBinary(blobFileInfo.Size())
						table.AddRow(ref, name, tag, digest[:12], size, created)
					}
				}
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(out, table.String())
	return err
}

// PullChart downloads a chart from a registry
func PullChart(out io.Writer, storageRootDir string, ref string) error {
	if err := validateRef(ref); err != nil {
		return err
	}

	destDir := filepath.Join(storageRootDir, "blobs", "sha256")
	os.MkdirAll(destDir, 0755)

	ctx := context.Background()
	resolver := docker.NewResolver(docker.ResolverOptions{})
	memoryStore := content.NewMemoryStore()

	fmt.Fprintf(out, "Pulling %s\n", ref)
	allowedMediaTypes := []string{HelmChartPackageMediaType}
	pullContents, err := oras.Pull(ctx, resolver, ref, memoryStore, allowedMediaTypes...)
	if err != nil {
		return err
	}

	os.MkdirAll(destDir, 0755)
	name, tag := getRefParts(ref)

	for _, descriptor := range pullContents {
		digest := descriptor.Digest.Hex()
		fmt.Fprintf(out, "sha256: %s\n", digest)
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
		tagDir := filepath.Join(storageRootDir, "refs", name)
		os.MkdirAll(tagDir, 0755)
		tagPath := filepath.Join(tagDir, tag)
		os.Remove(tagPath)
		err = os.Symlink(blobPath, tagPath)
		if err != nil {
			return err
		}
	}

	return nil
}

// PushChart uploads a chart to a registry
func PushChart(out io.Writer, storageRootDir string, ref string) error {
	if err := validateRef(ref); err != nil {
		return err
	}

	name, tag := getRefParts(ref)
	blobLink := filepath.Join(storageRootDir, "refs", name, tag)
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
	resolver := docker.NewResolver(docker.ResolverOptions{})
	memoryStore := content.NewMemoryStore()

	desc := memoryStore.Add(digest, HelmChartPackageMediaType, fileContent)
	pushContents := []ocispec.Descriptor{desc}

	fmt.Fprintf(out, "Pushing %s\nsha256: %s\n", ref, digest)
	return oras.Push(ctx, resolver, ref, memoryStore, pushContents)
}

// RemoveChart deletes a locally saved chart
func RemoveChart(out io.Writer, storageRootDir string, ref string) error {
	if err := validateRef(ref); err != nil {
		return err
	}

	name, tag := getRefParts(ref)
	blobLink := filepath.Join(storageRootDir, "refs", name, tag)

	fmt.Fprintf(out, "Deleting %s\n", ref)
	return os.Remove(blobLink)
}

// SaveChart packages a chart directory and stores a copy locally
func SaveChart(out io.Writer, storageRootDir string, path string, ref string) error {
	if err := validateRef(ref); err != nil {
		return err
	}

	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	ch, err := loader.LoadDir(path)
	if err != nil {
		return err
	}

	destDir := filepath.Join(storageRootDir, "blobs", "sha256")
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

	name, tag := getRefParts(ref)
	blobLinkParentDir := filepath.Join(storageRootDir, "refs", name)
	os.MkdirAll(blobLinkParentDir, 0755)
	blobLink := filepath.Join(blobLinkParentDir, tag)
	os.Remove(blobLink)
	err = os.Symlink(blobPath, blobLink)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "repo: %s\ntag: %s\ndigest: %s\n", name, tag, digest)
	return nil
}

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

func validateRef(ref string) error {
	// TODO validate this more!
	parts := strings.Split(ref, ":")
	if len(parts) < 2 {
		return errors.New("ref should be in the format name[:tag]")
	}
	return nil
}

func getRefParts(ref string) (string, string) {
	parts := strings.Split(ref, ":")
	lastIndex := len(parts) - 1
	refName := strings.Join(parts[0:lastIndex], ":")
	refTag := parts[lastIndex]
	return refName, refTag
}
