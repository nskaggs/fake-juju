package service_test

import (
	"path/filepath"
	"testing"
	"time"

	gc "gopkg.in/check.v1"

	"github.com/juju/juju/jujuclient"
	"github.com/juju/juju/state"
	"github.com/juju/juju/testing/factory"
	"github.com/juju/utils"

	coretesting "github.com/juju/juju/juju/testing"
	jujutesting "github.com/juju/juju/testing"

	"../service"
)

type FakeJujuServiceSuite struct {
	coretesting.JujuConnSuite
	service *service.FakeJujuService
}

func (s *FakeJujuServiceSuite) SetUpTest(c *gc.C) {
	s.JujuConnSuite.SetUpTest(c)

	options := &service.FakeJujuOptions{
		Mongo: -1, // Use the MongoDB instance that the suite will setup
	}
	s.service = service.NewFakeJujuService(s.BackingState, s.APIState, options)
}

// The Initialize() method performs various initialization tasks.
func (s *FakeJujuServiceSuite) TestInitialize(c *gc.C) {
	err := s.service.Initialize()
	c.Assert(err, gc.IsNil)

	// We want to be able to access the charm store
	c.Assert(utils.OutgoingAccessAllowed, gc.Equals, true)

	// There's a space defined
	ports, err := s.State.APIHostPorts()
	c.Assert(err, gc.IsNil)
	c.Assert(string(ports[0][0].SpaceName), gc.Equals, "dummy-provider-network")
}

// The WriteJujuData() method writes config files to a directory that
// can be used as JUJU_DATA for using command line tools against fake juju.
func (s *FakeJujuServiceSuite) TestWriteJujuData(c *gc.C) {
	path := c.MkDir()

	err := s.service.WriteJujuData(s.Environ, s.ControllerConfig, path)
	c.Assert(err, gc.IsNil)

	controllers, err := jujuclient.ReadControllersFile(
		filepath.Join(path, "controllers.yaml"))
	c.Assert(err, gc.IsNil)
	c.Assert(controllers.CurrentController, gc.Equals, "fake-juju")
}

// It's possible to start and stop the service.
func (s *FakeJujuServiceSuite) TestStart(c *gc.C) {
	s.service.Start()
	err := s.service.Stop()
	c.Assert(err, gc.IsNil)
}

// New machines in a pending state are automatically started.
func (s *FakeJujuServiceSuite) TestDeltaMachineChangedStart(c *gc.C) {
	s.Factory.MakeUnprovisionedMachineReturningPassword(c, &factory.MachineParams{
		Jobs:   []state.MachineJob{state.JobHostUnits},
		Series: "xenial",
	})
	s.service.Start()
	defer s.service.Stop()

	machine, err := s.BackingState.Machine("0")
	c.Assert(err, gc.IsNil)
	s.BackingState.StartSync()
	err = machine.WaitAgentPresence(2 * time.Second)
	c.Assert(err, gc.IsNil)

	alive, err := machine.AgentPresence()
	c.Assert(err, gc.IsNil)
	c.Assert(alive, gc.Equals, true)
}

var _ = gc.Suite(&FakeJujuServiceSuite{})

// Hook up gocheck into the "go test" runner.
func Test(t *testing.T) {
	jujutesting.MgoTestPackage(t)
}
