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

package registry // import "helm.sh/helm/pkg/registry"

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	orascontent "github.com/deislabs/oras/pkg/content"
	orascontext "github.com/deislabs/oras/pkg/context"
	"github.com/deislabs/oras/pkg/oras"
	"github.com/gosuri/uitable"
	"github.com/sirupsen/logrus"

	"helm.sh/helm/pkg/chart"
)

const (
	CredentialsFileBasename = "config.json"
)

type (
	// ClientOptions is used to construct a new client
	ClientOptions struct {
		Debug        bool
		Out          io.Writer
		Authorizer   Authorizer
		Resolver     Resolver
		CacheRootDir string
	}

	// Client works with OCI-compliant registries and local Helm chart cache
	Client struct {
		debug      bool
		out        io.Writer
		authorizer Authorizer
		resolver   Resolver
		cache      *filesystemCache // TODO: something more robust
	}
)

// NewClient returns a new registry client with config
func NewClient(options *ClientOptions) *Client {
	return &Client{
		debug:      options.Debug,
		out:        options.Out,
		resolver:   options.Resolver,
		authorizer: options.Authorizer,
		cache: &filesystemCache{
			out:     options.Out,
			rootDir: options.CacheRootDir,
			store:   orascontent.NewMemoryStore(),
		},
	}
}

// Login logs into a registry
func (c *Client) Login(hostname string, username string, password string) error {
	err := c.authorizer.Login(c.newContext(), hostname, username, password)
	if err != nil {
		return err
	}
	fmt.Fprint(c.out, "Login succeeded\n")
	return nil
}

// Logout logs out of a registry
func (c *Client) Logout(hostname string) error {
	err := c.authorizer.Logout(c.newContext(), hostname)
	if err != nil {
		return err
	}
	fmt.Fprint(c.out, "Logout succeeded\n")
	return nil
}

// PushChart uploads a chart to a registry
func (c *Client) PushChart(ref *Reference) error {
	fmt.Fprintf(c.out, "The push refers to repository [%s]\n", ref.Repo)
	layers, err := c.cache.LoadReference(ref)
	if err != nil {
		return err
	}
	_, err = oras.Push(c.newContext(), c.resolver, ref.String(), c.cache.store, layers,
		oras.WithConfigMediaType(HelmChartConfigMediaType))
	if err != nil {
		return err
	}
	var totalSize int64
	for _, layer := range layers {
		totalSize += layer.Size
	}
	fmt.Fprintf(c.out,
		"%s: pushed to remote (%s total)\n", ref.Tag, byteCountBinary(totalSize))
	return nil
}

// PullChart downloads a chart from a registry
func (c *Client) PullChart(ref *Reference) error {
	fmt.Fprintf(c.out, "%s: Pulling from %s\n", ref.Tag, ref.Repo)
	config, layers, err := oras.Pull(c.newContext(), c.resolver, ref.String(), c.cache.store, oras.WithAllowedMediaTypes(KnownMediaTypes()))
	if err != nil {
		return err
	}
	exists, err := c.cache.StoreReference(ref, config, layers)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Fprintf(c.out, "Status: Downloaded newer chart for %s:%s\n", ref.Repo, ref.Tag)
	} else {
		fmt.Fprintf(c.out, "Status: Chart is up to date for %s:%s\n", ref.Repo, ref.Tag)
	}
	return nil
}

// SaveChart stores a copy of chart in local cache
func (c *Client) SaveChart(ch *chart.Chart, ref *Reference) error {
	config, layers, err := c.cache.ChartToLayers(ch)
	if err != nil {
		return err
	}
	_, err = c.cache.StoreReference(ref, config, layers)
	if err != nil {
		return err
	}

	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		Config: config,
		Layers: layers,
	}

	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}

	manifestDescriptor := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestRaw),
		Size:      int64(len(manifestRaw)),
		Annotations: map[string]string{
			"org.opencontainers.image.ref.name": fmt.Sprintf("%s:%s", ref.Repo, ref.Tag),
		},
	}

	_, manifestPath := digestPath(filepath.Join(c.cache.rootDir, "blobs"), manifestDescriptor.Digest)

	err = writeFile(manifestPath, manifestRaw)
	if err != nil {
		return err
	}

	err = updateIndexJson(c.cache.rootDir, manifestDescriptor)
	if err != nil {
		return err
	}

	fmt.Fprintf(c.out, "Manifest Digest:  %s\n", manifestDescriptor.Digest.Hex())
	return nil
}

func updateIndexJson(cacheRootDir string, manifest ocispec.Descriptor) error {
	indexJsonFilePath := filepath.Join(cacheRootDir, "index.json")
	if _, err := os.Stat(indexJsonFilePath); os.IsNotExist(err) {
		tmpIndex := ocispec.Index{}
		tmpIndexRaw, err := json.Marshal(tmpIndex)
		if err != nil {
			return err
		}
		err = ioutil.WriteFile(indexJsonFilePath, tmpIndexRaw, 0644)
		if err != nil {
			return err
		}
	}

	indexJsonRaw, err := ioutil.ReadFile(indexJsonFilePath)
	if err != nil {
		return err
	}

	var origIndex ocispec.Index
	err = json.Unmarshal(indexJsonRaw, &origIndex)
	if err != nil {
		return err
	}

	origIndex.Manifests = append(origIndex.Manifests, manifest)

	index := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		Manifests: origIndex.Manifests,
	}
	indexRaw, err := json.Marshal(index)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(filepath.Join(cacheRootDir, "index.json"), indexRaw, 0644)
	return err
}

// LoadChart retrieves a chart object by reference
func (c *Client) LoadChart(ref *Reference) (*chart.Chart, error) {
	layers, err := c.cache.LoadReference(ref)
	if err != nil {
		return nil, err
	}
	ch, err := c.cache.LayersToChart(layers)
	return ch, err
}

// RemoveChart deletes a locally saved chart
func (c *Client) RemoveChart(ref *Reference) error {
	err := c.cache.DeleteReference(ref)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s: removed\n", ref.Tag)
	return err
}

// PrintChartTable prints a list of locally stored charts
func (c *Client) PrintChartTable() error {
	table := uitable.New()
	table.MaxColWidth = 60
	table.AddRow("REF", "NAME", "VERSION", "DIGEST", "SIZE", "CREATED")
	rows, err := c.cache.TableRows()
	if err != nil {
		return err
	}
	for _, row := range rows {
		table.AddRow(row...)
	}
	fmt.Fprintln(c.out, table.String())
	return nil
}

// disable verbose logging coming from ORAS unless debug is enabled
func (c *Client) newContext() context.Context {
	if !c.debug {
		return orascontext.Background()
	}
	ctx := orascontext.WithLoggerFromWriter(context.Background(), c.out)
	orascontext.GetLogger(ctx).Logger.SetLevel(logrus.DebugLevel)
	return ctx
}
