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
	"io"

	"github.com/containerd/containerd/remotes/docker"
	"github.com/spf13/cobra"

	"k8s.io/helm/cmd/helm/require"
	"k8s.io/helm/pkg/helm/helmpath"
	"k8s.io/helm/pkg/registry"
)

const chartRemoveDesc = `
TODO
`

type chartRemoveOptions struct {
	ref  string
	home helmpath.Home
}

func newChartRemoveCmd(out io.Writer) *cobra.Command {
	o := &chartRemoveOptions{}

	cmd := &cobra.Command{
		Use:     "remove [ref]",
		Aliases: []string{"rm"},
		Short:   "remove a chart",
		Long:    chartRemoveDesc,
		Args:    require.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.home = settings.Home
			o.ref = args[0]
			return o.run(out)
		},
	}

	return cmd
}

func (o *chartRemoveOptions) run(out io.Writer) error {
	resolver := registry.Resolver{
		Resolver: docker.NewResolver(docker.ResolverOptions{}),
	}

	registryClient := registry.Client{
		Resolver:       resolver,
		StorageRootDir: o.home.Registry(),
		Writer:         out,
	}

	ref, err := registry.ParseReference(o.ref)
	if err != nil {
		return err
	}

	return registryClient.RemoveChart(ref)
}
