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

package main

import (
	"fmt"
	"github.com/docker/go-units"
	"github.com/gosuri/uitable"
	"os"
	"path/filepath"
	"strings"
	"time"

	// "fmt"
	"github.com/spf13/cobra"
	"io"
	"k8s.io/helm/pkg/helm/helmpath"
)

const chartsDesc = `
TODO
`

type chartsOptions struct {
	home helmpath.Home
}

func newChartsCmd(out io.Writer) *cobra.Command {
	o := &chartsOptions{}

	cmd := &cobra.Command{
		Use:   "charts",
		Short: "list all charts in local cache",
		Long:  chartsDesc,
		Args: func(cmd *cobra.Command, args []string) error {
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			o.home = settings.Home
			return o.run(out)
		},
	}

	return cmd
}

// TODO: move alot of this to pkg/
func (o *chartsOptions) run(out io.Writer) error {
	table := uitable.New()
	table.MaxColWidth = 60
	table.AddRow("REPOSITORY", "TAG", "CHART ID", "CREATED", "SIZE")

	var ff = func(pathX string, infoX os.FileInfo, errX error) error {
		blobPath, err := os.Readlink(pathX) // check if this is a symlink (tag)
		if err == nil {
			blobFileInfo, err := os.Stat(blobPath)
			if err == nil {
				tag := filepath.Base(pathX)
				repo := strings.TrimRight(strings.TrimSuffix(pathX, tag), "/\\")
				if base := filepath.Base(blobPath); len(base) == 64 {
					id := base[:12]
					created := units.HumanDuration(time.Now().UTC().Sub(blobFileInfo.ModTime()))
					size := byteCountBinary(blobFileInfo.Size())
					table.AddRow(repo, tag, id, created, size)
				}
			}
		}
		return nil
	}

	os.Chdir(o.home.Registry())
	filepath.Walk(".", ff)

	fmt.Fprintln(out, table.String())
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
