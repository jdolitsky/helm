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

package registry // import "helm.sh/helm/internal/experimental/registry"

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/content"
	orascontext "github.com/deislabs/oras/pkg/context"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"helm.sh/helm/pkg/chart"
	"helm.sh/helm/pkg/chartutil"
)

const (
	CredentialsFileBasename = "config.json"
)

type (
	// ClientOptions is used to construct a new client
	ClientOptions struct {
		Debug      bool
		Out        io.Writer
		Authorizer Authorizer
		Resolver   Resolver
		Cache      Cache
	}

	// Client works with OCI-compliant registries and local Helm chart cache
	Client struct {
		debug      bool
		out        io.Writer
		authorizer Authorizer
		resolver   Resolver
		cache      Cache
	}
)

// NewClient returns a new registry client with config
func NewClient(options *ClientOptions) *Client {
	return &Client{
		debug:      options.Debug,
		out:        options.Out,
		resolver:   options.Resolver,
		authorizer: options.Authorizer,
		cache:      options.Cache,
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
	return nil
}

// PullChart downloads a chart from a registry
func (c *Client) PullChart(ref *Reference) error {
	return nil
}

// SaveChart stores a copy of chart in local cache
func (c *Client) SaveChart(ch *chart.Chart, ref *Reference) error {
	config, err := c.saveChartConfig(ch)
	if err != nil {
		return err
	}

	contentLayer, err := c.saveChartContentLayer(ch)
	if err != nil {
		return err
	}

	manifest, err := c.saveChartManifest(config, contentLayer)
	if err != nil {
		return err
	}

	c.cache.ociStore.AddReference(ref.FullName(), *manifest)
	err = c.cache.ociStore.SaveIndex()
	return err
}

// LoadChart retrieves a chart object by reference
func (c *Client) LoadChart(ref *Reference) (*chart.Chart, error) {
	return nil, nil
}

// RemoveChart deletes a locally saved chart
func (c *Client) RemoveChart(ref *Reference) error {
	c.cache.ociStore.DeleteReference(ref.FullName())
	err := c.cache.ociStore.SaveIndex()
	return err
}

// PrintChartTable prints a list of locally stored charts
func (c *Client) PrintChartTable() error {
	return nil
}

// store the Chart.yaml as json blob and return descriptor
func (c *Client) saveChartConfig(ch *chart.Chart) (*ocispec.Descriptor, error) {
	configBytes, err := json.Marshal(ch.Metadata)
	if err != nil {
		return nil, err
	}

	err = c.storeBlob(configBytes)
	if err != nil {
		return nil, err
	}

	desc := c.cache.memoryStore.Add("", HelmChartConfigMediaType, configBytes)
	return &desc, nil
}

// store the chart as tarball blob and return descriptor
func (c *Client) saveChartContentLayer(ch *chart.Chart) (*ocispec.Descriptor, error) {
	destDir := mkdir(filepath.Join(c.cache.rootDir, ".build"))
	tmpFile, err := chartutil.Save(ch, destDir)
	defer os.Remove(tmpFile)
	if err != nil {
		return nil, errors.Wrap(err, "failed to save")
	}
	contentBytes, err := ioutil.ReadFile(tmpFile)
	if err != nil {
		return nil, err
	}

	err = c.storeBlob(contentBytes)
	if err != nil {
		return nil, err
	}

	desc := c.cache.memoryStore.Add("", HelmChartContentLayerMediaType, contentBytes)
	return &desc, nil
}

// store the chart manifest as json blob and return descriptor
func (c *Client) saveChartManifest(config *ocispec.Descriptor, contentLayer *ocispec.Descriptor) (*ocispec.Descriptor, error) {
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		Config: *config,
		Layers: []ocispec.Descriptor{*contentLayer},
	}

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}

	err = c.storeBlob(manifestBytes)
	if err != nil {
		return nil, err
	}

	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}
	return &desc, nil
}

// store a blob on filesystem
func (c *Client) storeBlob(blobBytes []byte) error {
	writer, err := c.cache.ociStore.Store.Writer(c.newContext(),
		content.WithRef(digest.FromBytes(blobBytes).Hex()))
	if err != nil {
		return err
	}
	_, err = writer.Write(blobBytes)
	if err != nil {
		return err
	}
	err = writer.Commit(c.newContext(), 0, writer.Digest())
	if err != nil {
		return err
	}
	err = writer.Close()
	return err
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

// mkdir will create a directory (no error check) and return the path
func mkdir(dir string) string {
	os.MkdirAll(dir, 0755)
	return dir
}
