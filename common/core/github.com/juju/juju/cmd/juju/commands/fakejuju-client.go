// Simple HTTP client for the fake-jujud control-plan API

package commands

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
)

// Perform a POST HTTP request against the given fake-juju control plane path.
func postFakeJujuRequest(path string) error {
	port, err := getFakeJujudPort()
	if err != nil {
		return err
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: transport}

	url := fmt.Sprintf("https://127.0.0.1:%d/fake/%s", port, path)
	request, err := http.NewRequest("POST", url, bytes.NewBuffer([]byte("")))
	if err != nil {
		return err
	}

	logger.Debugf("Performing fake-juju request at %s", url)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	logger.Debugf("Got response Status: %s", response.Status)

	if response.StatusCode != 200 {
		return errors.New("Failed fake-juju request")
	}

	return nil
}
