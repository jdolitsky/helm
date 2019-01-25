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
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"k8s.io/helm/cmd/helm/require"
	"k8s.io/helm/pkg/registry"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/remotes/docker"
	"github.com/shizhMSFT/oras/pkg/content"
	"github.com/shizhMSFT/oras/pkg/oras"
	"github.com/spf13/cobra"
	"k8s.io/helm/pkg/helm/helmpath"
)

const chartPullDesc = `
TODO
`

type chartPullOptions struct {
	ref  string
	home helmpath.Home
}

func newChartPullCmd(out io.Writer) *cobra.Command {
	o := &chartPullOptions{}

	cmd := &cobra.Command{
		Use:   "pull [ref]",
		Short: "pull a chart from remote",
		Long:  chartPullDesc,
		Args:  require.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.home = settings.Home
			o.ref = args[0]
			return o.run(out)
		},
	}

	return cmd
}

func (o *chartPullOptions) run(out io.Writer) error {
	// 1. Create resolver
	// 2. Make sure o.ref resolves
	// 3. Attempt pull chart and validate
	// 4. save chart into HELM_HOME

	parts := strings.Split(o.ref, ":")
	if len(parts) < 2 {
		return errors.New("ref should be in the format name[:tag|@digest]")
	}

	lastIndex := len(parts) - 1
	refName := strings.Join(parts[0:lastIndex], ":")
	refTag := parts[lastIndex]

	destDir := filepath.Join(o.home.Registry(), "blobs", "sha256")
	os.MkdirAll(destDir, 0755)

	ctx := context.Background()
	resolver := docker.NewResolver(docker.ResolverOptions{})
	memoryStore := content.NewMemoryStore()

	fmt.Fprintf(out, "Pulling %s\n", o.ref)
	allowedMediaTypes := []string{registry.HelmChartPackageMediaType}
	pullContents, err := oras.Pull(ctx, resolver, o.ref, memoryStore, allowedMediaTypes...)
	if err != nil {
		return err
	}

	os.MkdirAll(destDir, 0755)

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

		tagDir := filepath.Join(o.home.Registry(), "refs", refName)
		os.MkdirAll(tagDir, 0755)
		tagPath := filepath.Join(tagDir, refTag)
		os.Remove(tagPath)
		err = os.Symlink(blobPath, tagPath)
		if err != nil {
			return err
		}
	}

	return nil
}
