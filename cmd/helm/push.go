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
	"fmt"
	"io"
	"io/ioutil"

	"github.com/containerd/containerd/remotes/docker"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/shizhMSFT/oras/pkg/content"
	"github.com/shizhMSFT/oras/pkg/oras"
	"github.com/spf13/cobra"

	"k8s.io/helm/cmd/helm/require"
)

const pushDesc = `
TODO
`

type pushOptions struct {
	file string
	ref  string
}

func newPushCmd(out io.Writer) *cobra.Command {
	o := &pushOptions{}

	cmd := &cobra.Command{
		Use:   "push [file] [ref] [...]",
		Short: "upload a chart to a registry",
		Long:  pushDesc,
		Args:  require.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.file = args[0]
			o.ref = args[1]
			return o.run(out)
		},
	}

	return cmd
}

func (o *pushOptions) run(out io.Writer) error {
	fileContent, err := ioutil.ReadFile(o.file)
	if err != nil {
		return err
	}

	ctx := context.Background()
	resolver := docker.NewResolver(docker.ResolverOptions{})
	memoryStore := content.NewMemoryStore()

	desc := memoryStore.Add(o.file, "application/vnd.helm.chart", fileContent)
	pushContents := []ocispec.Descriptor{desc}

	fmt.Printf("Pushing %s to %s\n", o.file, o.ref)
	return oras.Push(ctx, resolver, o.ref, memoryStore, pushContents)
}
