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
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/go-units"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// getAllChartRefs returns a map of all refs stored in a refsRootDir
func getAllChartRefs(refsRootDir string) (map[string]map[string]string, error) {
	refs := map[string]map[string]string{}

	// Walk the storage dir, check for symlinks under "refs" dir pointing to valid files in "blobs/" and "charts/"
	err := filepath.Walk(refsRootDir, func(path string, fileInfo os.FileInfo, fileError error) error {

		// Check if this file is a symlink
		linkPath, err := os.Readlink(path)
		if err == nil {
			destFileInfo, err := os.Stat(linkPath)
			if err == nil {
				tagDir := filepath.Dir(path)

				// Determine the ref
				ref := fmt.Sprintf("%s:%s", strings.TrimLeft(
					strings.TrimPrefix(filepath.Dir(filepath.Dir(tagDir)), refsRootDir), "/\\"),
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

	// Filter out any refs that are incomplete (do not have all required fields)
	for k, ref := range refs {
		allKeysFound := true
		for _, v := range []string{"name", "version", "digest", "size", "created"} {
			if _, ok := ref[v]; !ok {
				allKeysFound = false
				break
			}
		}
		if !allKeysFound {
			delete(refs, k)
		}
	}

	return refs, err
}

// splitDigest returns a sha256 digest in two parts, on with first 2 chars and one with second 62 chars
func splitDigest(digest string) (string, string) {
	var digestLeft, digestRight string
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) == 64 {
		digestLeft = digest[0:2]
		digestRight = digest[2:64]
	}
	return digestLeft, digestRight
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

// setLayerAnnotationsFromChartLink will set chart name/version annotations on a layer
// based on the path of the chart link destination
func setLayerAnnotationsFromChartLink(layer ocispec.Descriptor, chartLinkPath string) error {
	src, err := os.Readlink(chartLinkPath)
	if err != nil {
		return err
	}
	// example path: /some/path/charts/mychart/versions/1.2.0
	chartName := filepath.Base(filepath.Dir(filepath.Dir(src)))
	chartVersion := filepath.Base(src)
	layer.Annotations[HelmChartNameAnnotation] = chartName
	layer.Annotations[HelmChartVersionAnnotation] = chartVersion
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
