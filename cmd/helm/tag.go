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
	"bytes"
	"fmt"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"io"
	"io/ioutil"
	"k8s.io/helm/cmd/helm/require"
	"k8s.io/helm/pkg/chart/loader"
	"k8s.io/helm/pkg/chartutil"
	"k8s.io/helm/pkg/helm/helmpath"
	"k8s.io/helm/pkg/provenance"
	"os"
	"path/filepath"
	"strings"
)

const tagDesc = `
TODO
`

type tagOptions struct {
	path string
	ref  string
	home helmpath.Home
}

func newTagCmd(out io.Writer) *cobra.Command {
	o := &tagOptions{}

	cmd := &cobra.Command{
		Use:   "tag [path] [ref] [...]",
		Short: "assign chart to ref in local cache",
		Long:  tagDesc,
		Args:  require.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.home = settings.Home
			o.path = args[0]
			o.ref = args[1]
			return o.run(out)
		},
	}

	return cmd
}

// TODO: move alot of this to pkg/
func (o *tagOptions) run(out io.Writer) error {
	path, err := filepath.Abs(o.path)
	if err != nil {
		return err
	}

	ch, err := loader.LoadDir(path)
	if err != nil {
		return err
	}

	parts := strings.Split(o.ref, ":")
	if len(parts) < 2 {
		return errors.New("ref should be in the format name[:tag|@digest]")
	}

	lastIndex := len(parts) - 1
	refName := strings.Join(parts[0:lastIndex], ":")
	refTag := parts[lastIndex]

	destDir := filepath.Join(o.home.Registry(), refName)
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

	digestFile := filepath.Join(destDir, digest)
	err = os.Rename(tmpFile, digestFile)
	if err != nil {
		return err
	}

	tagFile := filepath.Join(destDir, refTag)
	os.Remove(tagFile)
	err = os.Symlink(digestFile, tagFile)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "repo: %s\ntag: %s\ndigest: %s\n", refName, refTag, digest)
	return nil
}
