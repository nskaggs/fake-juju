package service

import (
	"os"
	"os/signal"
	"syscall"

	gc "gopkg.in/check.v1"
	corecharm "gopkg.in/juju/charmrepo.v2-unstable"

	"github.com/juju/juju/juju/testing"
	"github.com/juju/juju/state"
	"github.com/juju/juju/testing/factory"

	jc "github.com/juju/testing/checkers"
)

// Wrapper to setup and run the core FakeJujuService.
//
// It's implemented as a gocheck test suite because that's the easiest way
// to re-use all the code that sets up the dummy provider. Ideally such
// logic should be factored out from testing-related tooling and be made
// standalone.
type FakeJujuSuite struct {
	testing.JujuConnSuite

	options *FakeJujuOptions
	service *FakeJujuService
}

func (s *FakeJujuSuite) SetUpTest(c *gc.C) {
	log.Infof("Initializing test suite")
	s.JujuConnSuite.SetUpTest(c)

	s.PatchValue(&corecharm.CacheDir, c.MkDir())

	s.service = NewFakeJujuService(s.BackingState, s.APIState, s.options)

	// Create machine 0
	log.Infof("Creating controller machine")
	s.Factory.MakeUnprovisionedMachineReturningPassword(c, &factory.MachineParams{
		Jobs:   []state.MachineJob{state.JobHostUnits},
		Series: "xenial",
	})

	// Initialize the service
	err := s.service.Initialize()
	c.Assert(err, jc.ErrorIsNil)

	// Dump controller and model info to the given JUJU_DATA dir.
	if s.options.JujuData == "" {
		s.options.JujuData = c.MkDir()
	}
	err = s.service.WriteJujuData(
		s.Environ, s.ControllerConfig, s.options.JujuData)
	c.Assert(err, jc.ErrorIsNil)
}

func (s *FakeJujuSuite) TestStart(c *gc.C) {
	log.Infof(
		"service is ready: run 'export JUJU_DATA=%s' to use the regular juju cli",
		s.options.JujuData)

	s.service.Start()

	// TODO: implement actual fake-juju logic. For now we just wait forever
	// until SIGINT (ctrl-c) or SIGTERM is received.
	terminate := make(chan os.Signal, 2)
	signal.Notify(terminate, os.Interrupt, syscall.SIGTERM)
	var err error
	select {
	case <-terminate:
	case err = <-s.service.errored:
	}
	if err == nil {
		// This means we received a signal, let's shutdown cleanly
		log.Infof("Terminating service")
		err = s.service.Stop()
	}
	c.Assert(err, jc.ErrorIsNil)
}
