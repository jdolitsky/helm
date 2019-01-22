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
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/remotes/docker"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/shizhMSFT/oras/pkg/content"
	"github.com/shizhMSFT/oras/pkg/oras"
	"github.com/spf13/cobra"
	"k8s.io/helm/pkg/helm/helmpath"

	"k8s.io/helm/cmd/helm/require"
)

const pushDesc = `
TODO
`

type pushOptions struct {
	ref  string
	home helmpath.Home
}

func newPushCmd(out io.Writer) *cobra.Command {
	o := &pushOptions{}

	cmd := &cobra.Command{
		Use:   "push [ref] [...]",
		Short: "upload a chart to a registry",
		Long:  pushDesc,
		Args:  require.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.home = settings.Home
			o.ref = args[0]
			return o.run(out)
		},
	}

	return cmd
}

func (o *pushOptions) run(out io.Writer) error {
	parts := strings.Split(o.ref, ":")
	if len(parts) < 2 {
		return errors.New("ref should be in the format name[:tag|@digest]")
	}

	lastIndex := len(parts) - 1
	refName := strings.Join(parts[0:lastIndex], ":")
	refTag := parts[lastIndex]

	blobLink := filepath.Join(o.home.Registry(), refName, refTag)
	fileContent, err := ioutil.ReadFile(blobLink)
	if err != nil {
		return err
	}

	ctx := context.Background()
	resolver := docker.NewResolver(docker.ResolverOptions{})
	memoryStore := content.NewMemoryStore()

	desc := memoryStore.Add("bingo", "application/vnd.helm.chart", fileContent)
	pushContents := []ocispec.Descriptor{desc}

	fmt.Fprintf(out, "Pushing %s\n", o.ref)
	return oras.Push(ctx, resolver, o.ref, memoryStore, pushContents)
}
