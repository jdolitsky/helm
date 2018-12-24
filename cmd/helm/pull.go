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
	"os"
	"strings"

	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
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

// adapted from https://gist.github.com/cpuguy83/541dc445fad44193068a1f8f365a9c0e
func (o *pullOptions) run(out io.Writer) error {
	parts := strings.Split(o.chartRef, ":")
	if len(parts) != 2 {
		return errors.New("invalid chart format, must be NAME[:TAG|@DIGEST]")
	}
	chartName := parts[0]
	chartVersion := parts[1]

	fmt.Printf("%s: Pulling from %s\n", chartVersion, chartName)

	cacheDir := settings.Home.Cache()
	os.MkdirAll(cacheDir, os.ModePerm)

	tmpdir, err := ioutil.TempDir(cacheDir, ".helm-pull")
	if err != nil {
		return err
	}
	defer func() {
		e := recover()
		os.RemoveAll(tmpdir)
		if e == nil {
			return
		}
		panic(e)
	}()

	// this store is a local content addressible store
	// it satifies the "Ingestor" interface used by the call to `images.Dispatch`
	cs, err := local.NewStore(cacheDir)
	if err != nil {
		return err
	}

	resolver := docker.NewResolver(docker.ResolverOptions{})
	ctx := context.Background()

	name, desc, err := resolver.Resolve(ctx, o.chartRef)
	if err != nil {
		return err
	}

	fmt.Printf("Digest: %s\n", desc.Digest)
	fmt.Printf("MediaType: %s\n", desc.MediaType)

	fetcher, err := resolver.Fetcher(ctx, name)
	if err != nil {
		return err
	}

	r, err := fetcher.Fetch(ctx, desc)
	if err != nil {
		return err
	}
	defer r.Close()

	// Handler which reads a descriptor and fetches the referenced data (e.g. image layers) from the remote, recursively
	h := images.Handlers(remotes.FetchHandler(cs, fetcher), images.ChildrenHandler(cs))

	// This traverses the OCI descriptor to fetch the image and store it into the local store initialized above.
	// All content hashes are verified in this step
	if err := images.Dispatch(ctx, h, desc); err != nil {
		return err
	}

	fmt.Printf("Status: Chart is up to date for %s\n", o.chartRef)
	return nil
}
