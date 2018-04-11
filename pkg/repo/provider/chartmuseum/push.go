/*
Copyright 2018 The Kubernetes Authors All rights reserved.

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

package chartmuseum // import "k8s.io/helm/pkg/repo/provider/chartmuseum"

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"k8s.io/helm/pkg/chartutil"
	"k8s.io/helm/pkg/version"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
)

// Push uploads a chart package to a ChartMuseum web server.
func (cm *ChartMuseum) Push(packageAbsPath string, namespace string) error {
	chart, err := chartutil.LoadFile(packageAbsPath)
	if err != nil {
		return err
	}

	meta := chart.GetMetadata()
	msg := fmt.Sprintf("Pushing chart %s version %s to repo %s", meta.Name, meta.Version, cm.Config.Name)
	if namespace != "" {
		msg += fmt.Sprintf("[%s]", namespace)
	}
	fmt.Println(msg + "...")

	return uploadPackage(packageAbsPath, cm.Config.URL, namespace, cm.Config.Username, cm.Config.Password, "")
}

func uploadPackage(packageAbsPath string, endpoint string, namespace string, username string, password string, token string) error {
	if token == "" {
		fmt.Println("[1] Attempt operation...")
	} else {
		fmt.Println("[5] Retry operation with access token...")
	}

	client := &http.Client{}

	u, err := url.Parse(endpoint)
	u.Path = path.Join("api", namespace, "charts")
	req, err := http.NewRequest("POST", u.String(), nil)
	if err != nil {
		return err
	}

	err = setUploadPackageRequestBody(req, packageAbsPath)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "Helm/"+strings.TrimPrefix(version.GetVersion(), "v"))

	//if username != "" && password != "" {
	//	req.SetBasicAuth(username, password)
	//}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	if token == "" && resp.StatusCode == 401 {
		fmt.Println("[2] Received 401 from chartmuseum with auth instructions...")

		wwwAuth := resp.Header.Get("WWW-Authenticate")
		if wwwAuth == "" {
			return errors.New("missing WWW-Authenticate header")
		}

		parts := strings.Split(resp.Header.Get("WWW-Authenticate"), "Bearer ")
		if len(parts) != 2 {
			return errors.New("malformed WWW-Authenticate header")
		}

		keys := map[string]string{}
		for _, v := range strings.Split(parts[1], "\",") {
			tmp := strings.Split(v, "=")
			if len(tmp) == 2 {
				keys[tmp[0]] = strings.Trim(tmp[1], "\"")
			}
		}

		realm, ok := keys["realm"]
		if !ok {
			return errors.New("No realm in WWW-Authenticate header")
		}

		service, ok := keys["service"]
		if !ok {
			return errors.New("No service in WWW-Authenticate header")
		}

		scope, ok := keys["scope"]
		if !ok {
			return errors.New("No scope in WWW-Authenticate header")
		}

		fmt.Println(fmt.Sprintf("[3] Requesting token from %s with scope %s...", realm, scope))
		token = "xyz"

		var jsonStr = []byte(fmt.Sprintf(`{
			"client_id": "xxx",
			"client_secret": "xxx",
			"audience": "%s",
			"grant_type": "password",
			"username": "xxx",
			"password": "xxx",
			"scope": "%s"
		}`, service, scope))

		req, err = http.NewRequest("POST", realm, bytes.NewBuffer(jsonStr))
		if err != nil {
			return err
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Helm/"+strings.TrimPrefix(version.GetVersion(), "v"))

		resp, err = client.Do(req)
		if err != nil {
			return err
		}

		b, err := ioutil.ReadAll(resp.Body)
		defer resp.Body.Close()
		if err != nil {
			return err
		}

		type t struct {
			AccessToken string `json:"access_token"`
			ExpiresIn   int    `json:"expires_in"`
			TokenType   string `json:"token_type"`
		}
		var tt t
		err = json.Unmarshal(b, &tt)
		if err != nil {
			return err
		}
		token = tt.AccessToken

		fmt.Println("[4] Received access token from auth server")

		return uploadPackage(packageAbsPath, endpoint, namespace, username, password, token)

	} else if resp.StatusCode != 201 {
		b, err := ioutil.ReadAll(resp.Body)
		defer resp.Body.Close()
		if err != nil {
			return err
		}
		var er errorResponse
		err = json.Unmarshal(b, &er)
		if err != nil || er.Error == "" {
			return errors.New(fmt.Sprintf("%d: could not properly parse response JSON: %s",
				resp.StatusCode, string(b)))
		}
		return errors.New(fmt.Sprintf("%d: %s", resp.StatusCode, er.Error))
	}

	fmt.Println("[6] Client authorized, operation successful")
	fmt.Println("Done.")
	return nil
}

func setUploadPackageRequestBody(req *http.Request, packageAbsPath string) error {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	defer w.Close()
	fw, err := w.CreateFormFile("chart", packageAbsPath)
	if err != nil {
		return err
	}
	w.FormDataContentType()
	fd, err := os.Open(packageAbsPath)
	if err != nil {
		return err
	}
	defer fd.Close()
	_, err = io.Copy(fw, fd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Body = ioutil.NopCloser(&body)
	return nil
}
