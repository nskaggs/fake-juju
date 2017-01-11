// Simple HTTP client for the fake-jujud control-plan API

package modelcmd

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/juju/errors"
)

// Perform a POST HTTP request against the given fake-juju control plane path.
func PostFakeJujuRequest(path string) error {
	port, err := GetFakeJujudPort()
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

// Figure the port that fake-jujud is listening to.
func GetFakeJujudPort() (port int, err error) {
	port = 17079 // the default
	if os.Getenv("FAKE_JUJUD_PORT") != "" {
		port, err = strconv.Atoi(os.Getenv("FAKE_JUJUD_PORT"))
		if err != nil {
			return 0, errors.Annotate(err, "invalid port number")
		}
	}
	return port, nil
}
