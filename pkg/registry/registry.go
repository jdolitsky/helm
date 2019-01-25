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
	"github.com/docker/go-units"
	"github.com/gosuri/uitable"
	"k8s.io/helm/pkg/helm/helmpath"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	HelmChartPackageMediaType = "application/vnd.cncf.helm.chart.v1.tar+gzip"
)

func TagChart() error {
	return nil
}

func PushChart() error {
	return nil
}

func PullChart() error {
	return nil
}

func ListCharts(home helmpath.Home) (string, error) {
	// 1. Create new ui table
	// 2. Obtain a pager
	// 3. for loop on pager, add rows
	// 4. print ui table

	table := uitable.New()
	table.MaxColWidth = 60
	table.AddRow("REF", "NAME", "VERSION", "DIGEST", "SIZE", "CREATED")

	var ff = func(pathX string, infoX os.FileInfo, errX error) error {
		blobPath, err := os.Readlink(pathX) // check if this is a symlink (tag)
		if err == nil {
			blobFileInfo, err := os.Stat(blobPath)
			if err == nil {
				tag := filepath.Base(pathX)
				repo := strings.TrimRight(strings.TrimSuffix(pathX, tag), "/\\")
				if digest := filepath.Base(blobPath); len(digest) == 64 {
					created := units.HumanDuration(time.Now().UTC().Sub(blobFileInfo.ModTime()))
					size := byteCountBinary(blobFileInfo.Size())
					name := filepath.Base(repo)
					table.AddRow(fmt.Sprintf("%s:%s", repo, tag), name, tag, digest[:12], size, created)
				}
			}
		}
		return nil
	}

	refsDir := filepath.Join(home.Registry(), "refs")
	os.MkdirAll(refsDir, 0755)
	err := os.Chdir(refsDir)
	if err != nil {
		return "", err
	}

	err = filepath.Walk(".", ff)
	if err != nil {
		return "", err
	}

	return table.String(), nil
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
