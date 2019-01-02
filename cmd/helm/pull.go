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
	"github.com/shizhMSFT/oras/pkg/oras"
	"github.com/spf13/cobra"

	"k8s.io/helm/cmd/helm/require"
)

const pullDesc = `
Retrieve a package from a package repository, and download it locally.

This is useful for fetching packages to inspect, modify, or repackage. It can
also be used to perform cryptographic verification of a chart without installing
the chart.

There are options for unpacking the chart after download. This will create a
directory for the chart and uncompress into that directory.

If the --verify flag is specified, the requested chart MUST have a provenance
file, and MUST pass the verification process. Failure in any part of this will
result in an error, and the chart will not be saved locally.
`

type pullOptions struct {
	destdir     string // --destination
	devel       bool   // --devel
	untar       bool   // --untar
	untardir    string // --untardir
	verifyLater bool   // --prov

	chartRef string

	chartPathOptions
}

func newPullCmd(out io.Writer) *cobra.Command {
	o := &pullOptions{}

	cmd := &cobra.Command{
		Use:     "pull [chart URL | repo/chartname] [...]",
		Short:   "download a chart from a repository and (optionally) unpack it in local directory",
		Aliases: []string{"fetch"},
		Long:    pullDesc,
		Args:    require.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if o.version == "" && o.devel {
				debug("setting version to >0.0.0-0")
				o.version = ">0.0.0-0"
			}

			for i := 0; i < len(args); i++ {
				o.chartRef = args[i]
				if err := o.run(out); err != nil {
					return err
				}
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.BoolVar(&o.devel, "devel", false, "use development versions, too. Equivalent to version '>0.0.0-0'. If --version is set, this is ignored.")
	f.BoolVar(&o.untar, "untar", false, "if set to true, will untar the chart after downloading it")
	f.BoolVar(&o.verifyLater, "prov", false, "fetch the provenance file, but don't perform verification")
	f.StringVar(&o.untardir, "untardir", ".", "if untar is specified, this flag specifies the name of the directory into which the chart is expanded")
	f.StringVarP(&o.destdir, "destination", "d", ".", "location to write the chart. If this and tardir are specified, tardir is appended to this")

	o.chartPathOptions.addFlags(f)

	return cmd
}

func (o *pullOptions) run(out io.Writer) error {
	ctx := context.Background()
	resolver := docker.NewResolver(docker.ResolverOptions{})

	fmt.Printf("Pulling %s\n", o.chartRef)

	allowedMediaTypes := []string{"application/vnd.helm.chart"}
	pullContents, err := oras.Pull(ctx, resolver, o.chartRef, allowedMediaTypes...)
	if err != nil {
		return err
	}

	for name, blob := range pullContents {
		fmt.Printf("Saving %s\n", name)
		err := ioutil.WriteFile(name, blob.Content, 0644)
		if err != nil {
			return err
		}
	}

	return nil
}
