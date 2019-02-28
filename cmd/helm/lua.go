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

	"github.com/spf13/cobra"

	"k8s.io/helm/pkg/action"
)

const luaHelp = `
TODO
`

func newLuaCmd(cfg *action.Configuration, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lua",
		Short: "lua, lua, or lua. also lua.",
		Long:  luaHelp,
	}
	cmd.AddCommand(
		newLuaRunCmd(cfg, out),
	)
	return cmd
}
