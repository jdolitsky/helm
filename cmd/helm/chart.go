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
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"io"
)

const chartHelp = `
This command consists of multiple subcommands to interact with charts and registries.

It can be used to push, pull, tag, list, or remove Helm charts.
Example usage:
    $ helm chart pull [URL]
`

func newChartCmd(out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chart",
		Short: "push, pull, tag, or remove Helm charts",
		Long:  chartHelp,
	}
	cmd.AddCommand(
		newChartListCmd(out),
		newChartPullCmd(out),
		newChartPushCmd(out),
		newChartRemoveCmd(out),
		newChartSaveCmd(out),
	)
	return cmd
}

// TODO remove once WARN lines removed from oras or containerd
func init() {
	logrus.SetLevel(logrus.ErrorLevel)
}