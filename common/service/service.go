package service

import (
	"fmt"
	"io"
	"os"

	"github.com/juju/juju/api"
	"github.com/juju/juju/controller"
	"github.com/juju/juju/environs"
	"github.com/juju/juju/instance"
	"github.com/juju/juju/juju/osenv"
	"github.com/juju/juju/jujuclient"
	"github.com/juju/juju/network"
	"github.com/juju/juju/state"
	"github.com/juju/juju/testing"
	"github.com/juju/juju/version"
	"github.com/juju/loggo"
	"github.com/juju/utils"

	semversion "github.com/juju/version"
)

// Runtime options for the fake-juju service
type FakeJujuOptions struct {
	Output io.Writer   // Where to write logs
	Level  loggo.Level // The log level

	// MongoDB port to connect to (on localhost). If set to 0,
	// a dedicated MongoDB child process will be spawned. If
	// set to -1, no setup will be done at all (for tests)
	Mongo int

	Cert   string // Path to the directory holding the certificates to use
	Series string // Default Ubuntu series

	// Path to a directory that will be populated with JUJU_DATA-compatible
	// files, so the regular command line client can be used with fake-juju.
	JujuData string
}

// The core fake-juju service
func NewFakeJujuService(
	state *state.State, api api.Connection, options *FakeJujuOptions) *FakeJujuService {

	return &FakeJujuService{
		state:   state,
		api:     api,
		options: options,
	}
}

type FakeJujuService struct {
	state         *state.State
	api           api.Connection
	options       *FakeJujuOptions
	instanceCount int
}

// Main initialization entry point
func (s *FakeJujuService) Initialize() error {

	// Juju needs internet access to reach the charm store.  This is
	// necessary to download charmstore charms (e.g. when adding a
	// service.  In the case of downloading charms, the controller
	// does not try to download if the charm is already in the database.
	// So internet access could go back to disabled if we were to add
	// the charm directly before using any API methods that try to
	// download it.  Here are some ideas on how to do that:
	//  * use the API's "upload charm" HTTP endpoint (POST on /charms);
	//    this requires disabling the prohibition against uploading
	//    charmstore charms (which we've done already in fake-juju);
	//    this could be accomplished using txjuju, python-jujuclient,
	//    or manually with httplib (all have auth complexity)
	//  * add an "upload-charm" command to fake-juju that forces a
	//    charmstore charm into the DB
	//  * add the service with a "local" charm schema and then forcibly
	//    change the charm's schema to "cs" in the DB
	// In the meantime, we allow fake-juju to have internet access.
	// XXX (lp:1639276): Remove this special case.
	utils.OutgoingAccessAllowed = true

	ports := s.api.APIHostPorts()
	ports[0][0].SpaceName = "dummy-provider-network"
	err := s.state.SetAPIHostPorts(ports)
	if err != nil {
		return err
	}

	config := map[string]interface{}{"default-series": s.options.Series}
	err = s.state.UpdateModelConfig(config, nil, nil)
	if err != nil {
		return err
	}

	return nil
}

// Initialize the controller machine (aka machine 0).
func (s *FakeJujuService) InitializeController(controller *state.Machine) error {
	currentVersion := version.Current.String()

	agentVersion, err := semversion.ParseBinary(currentVersion + "-xenial-amd64")
	if err != nil {
		return err
	}

	controller.SetAgentVersion(agentVersion)

	address := network.NewScopedAddress("127.0.0.1", network.ScopeCloudLocal)
	return controller.SetProviderAddresses(address)
}

func (s *FakeJujuService) NewInstanceId() instance.Id {
	s.instanceCount += 1
	return instance.Id(fmt.Sprintf("id-%d", s.instanceCount))
}

// Dump configuration files to the given JUJU_DATA directory.
// The environ object is typically provided by JujuConnSuite.Environ.
func (s *FakeJujuService) WriteJujuData(
	environ environs.Environ, config controller.Config, path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		err = os.Mkdir(path, 0755)
		if err != nil {
			return err
		}
	}

	uuid := config.ControllerUUID()
	port := config.APIPort()
	info, err := environs.APIInfo(
		uuid,
		testing.ModelTag.Id(),
		testing.CACert,
		port,
		environ)
	if err != nil {
		return err
	}

	osenv.SetJujuXDGDataHome(path)

	if err := s.writeJujuDataControllers(uuid, info.CACert, port); err != nil {
		return err
	}
	if err := s.writeJujuDataAccounts(); err != nil {
		return err
	}
	if err := s.writeJujuDataModels(); err != nil {
		return err
	}
	return nil
}

// Write controllers.yaml
func (s *FakeJujuService) writeJujuDataControllers(uuid, caCert string, port int) error {
	controllers := &jujuclient.Controllers{
		Controllers: map[string]jujuclient.ControllerDetails{
			"fake-juju": jujuclient.ControllerDetails{
				ControllerUUID: uuid,
				APIEndpoints:   []string{fmt.Sprintf("localhost:%d", port)},
				CACert:         caCert,
				AgentVersion:   version.Current.String(),
			},
		},
		CurrentController: "fake-juju",
	}
	return jujuclient.WriteControllersFile(controllers)
}

// Write accounts.yaml
func (s *FakeJujuService) writeJujuDataAccounts() error {
	accounts := map[string]jujuclient.AccountDetails{
		"fake-juju": jujuclient.AccountDetails{
			User:     "admin",
			Password: "dummy-secret",
		},
	}
	return jujuclient.WriteAccountsFile(accounts)
}

// Write models.yaml
func (s *FakeJujuService) writeJujuDataModels() error {

	models := &jujuclient.ControllerModels{
		Models: map[string]jujuclient.ModelDetails{
			"admin/controller": jujuclient.ModelDetails{
				ModelUUID: "deadbeef-0bad-400d-8000-4b1d0d06f00d",
			},
		},
		CurrentModel: "admin/controller",
	}
	return jujuclient.WriteModelsFile(map[string]*jujuclient.ControllerModels{
		"fake-juju": models,
	})
}
