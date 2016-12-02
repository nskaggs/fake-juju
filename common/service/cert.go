// Handle test certificates used by fake-juju

package service

import (
	"io/ioutil"
	"path/filepath"

	gitjujutesting "github.com/juju/testing"

	"github.com/juju/juju/cert"
	"github.com/juju/juju/testing"
)

var (
	filenames = []string{
		"ca.cert",
		"ca.key",
		"server.cert",
		"server.key",
	}

	certificates = []*string{
		&testing.CACert,
		&testing.CAKey,
		&testing.ServerCert,
		&testing.ServerKey,
	}
)

// Set the certificate global variables in the github.com/juju/juju/testing
// package, so the test suite uses a custom certificate instead of the
// generated one that would otherwise be set. This allows us to point to an
// external MongoDB process, spawned using the custom certificate.
func SetCerts(path string) error {

	log.Infof("Loading certificates from %s", path)

	for i, filename := range filenames {
		data, err := ioutil.ReadFile(filepath.Join(path, filename))
		if err != nil {
			return err
		}
		*certificates[i] = string(data)
	}

	caCertX509, _, err := cert.ParseCertAndKey(
		testing.CACert, testing.CAKey)
	if err != nil {
		return err
	}

	serverCert, serverKey, err := cert.ParseCertAndKey(
		testing.ServerCert, testing.ServerKey)

	if err != nil {
		return err
	}

	testing.Certs = &gitjujutesting.Certs{
		CACert:     caCertX509,
		ServerCert: serverCert,
		ServerKey:  serverKey,
	}

	return nil
}
