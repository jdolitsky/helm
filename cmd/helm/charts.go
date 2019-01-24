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
	"io"

	"github.com/spf13/cobra"

	"k8s.io/helm/pkg/helm/helmpath"
	"k8s.io/helm/pkg/registry"
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
		Short: "list all charts stored locally",
		Long:  chartsDesc,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.home = settings.Home
			return o.run(out)
		},
	}

	return cmd
}

func (o *chartsOptions) run(out io.Writer) error {
	table, err := registry.ListCharts(o.home)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, table)
	return err
}