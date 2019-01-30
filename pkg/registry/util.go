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
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/docker/go-units"
	checksum "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

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

// extractLayers obtains the meta and content layers from a list of layers
func extractLayers(layers []ocispec.Descriptor) (ocispec.Descriptor, ocispec.Descriptor, error) {
	var metaLayer, contentLayer ocispec.Descriptor

	if len(layers) != 2 {
		return metaLayer, contentLayer, errors.New("manifest does not contain exactly 2 layers")
	}

	for _, layer := range layers {
		switch layer.MediaType {
		case HelmChartMetaMediaType:
			metaLayer = layer
		case HelmChartContentMediaType:
			contentLayer = layer
		}
	}

	if metaLayer.Size == 0 {
		return metaLayer, contentLayer, errors.New("manifest does not contain a Helm chart meta layer")
	}

	if contentLayer.Size == 0 {
		return metaLayer, contentLayer, errors.New("manifest does not contain a Helm chart content layer")
	}

	return metaLayer, contentLayer, nil
}

// extractChartNameVersionFromLayer retrieves the chart name and version from layer annotations
func extractChartNameVersionFromLayer(layer ocispec.Descriptor) (string, string, error) {
	name, ok := layer.Annotations[HelmChartNameAnnotation]
	if !ok {
		return "", "", errors.New("could not find chart name in annotations")
	}
	version, ok := layer.Annotations[HelmChartVersionAnnotation]
	if !ok {
		return "", "", errors.New("could not find chart version in annotations")
	}
	return name, version, nil
}

// extractChartNameVersionFromRef retrieves the chart name and version from a Reference
func extractChartNameVersionFromRef(refsRootDir string, ref *Reference) (string, string, error) {
	chartPath, err := os.Readlink(filepath.Join(refsRootDir, ref.Locator, "tags", ref.Object, "chart"))
	if err != nil {
		return "", "", nil
	}
	name := filepath.Base(filepath.Dir(filepath.Dir(chartPath)))
	version := filepath.Base(chartPath)
	return name, version, nil
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

// createDigestFile calcultaes the sha256 digest of some content and creates a file, returning the path
func createDigestFile(rootDir string, c []byte) (string, error) {
	digest := checksum.FromBytes(c).String()
	digestLeft, digestRight := splitDigest(digest)
	pathDir := filepath.Join(rootDir, "sha256", digestLeft)
	path := filepath.Join(pathDir, digestRight)
	if _, err := os.Stat(path); err != nil && os.IsNotExist(err) {
		os.MkdirAll(pathDir, 0755)
		err := ioutil.WriteFile(path, c, 0644)
		if err != nil {
			return "", err
		}
	}
	return path, nil
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

// mkdir will create a directory (no error check) and return the path
func mkdir(dir string) string {
	os.MkdirAll(dir, 0755)
	return dir
}

// getRefsSorted returns a map of all refs stored in a refsRootDir
func getRefsSorted(refsRootDir string) ([]map[string]string, error) {
	refsMap := map[string]map[string]string{}

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
				if _, ok := refsMap[ref]; !ok {
					refsMap[ref] = map[string]string{}
				}

				// Add data to entry based on file name (symlink name)
				base := filepath.Base(path)
				switch base {
				case "chart":
					refsMap[ref]["name"] = filepath.Base(filepath.Dir(filepath.Dir(linkPath)))
					refsMap[ref]["version"] = destFileInfo.Name()
				case "content":
					shaPrefix := filepath.Base(filepath.Dir(linkPath))
					digest := fmt.Sprintf("%s%s", shaPrefix, destFileInfo.Name())

					// Make sure the filename looks like a sha256 digest (64 chars)
					if len(digest) == 64 {
						refsMap[ref]["digest"] = digest[:7]
						refsMap[ref]["size"] = byteCountBinary(destFileInfo.Size())
						refsMap[ref]["created"] = units.HumanDuration(time.Now().UTC().Sub(destFileInfo.ModTime()))
					}
				}
			}
		}

		return nil
	})

	// Filter out any refs that are incomplete (do not have all required fields)
	for k, ref := range refsMap {
		allKeysFound := true
		for _, v := range []string{"name", "version", "digest", "size", "created"} {
			if _, ok := ref[v]; !ok {
				allKeysFound = false
				break
			}
		}
		if !allKeysFound {
			delete(refsMap, k)
		}
	}

	// Sort and convert to slice
	var refs []map[string]string
	keys := make([]string, 0, len(refsMap))
	for key := range refsMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		ref := refsMap[key]
		ref["ref"] = key
		refs = append(refs, ref)
	}

	return refs, err
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
