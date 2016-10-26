package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	gc "gopkg.in/check.v1"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/juju/juju/agent"
	"github.com/juju/juju/api"
	"github.com/juju/juju/cmd/juju/controller"
	"github.com/juju/juju/instance"
	"github.com/juju/juju/juju/osenv"
	jujutesting "github.com/juju/juju/juju/testing"
	"github.com/juju/juju/jujuclient"
	"github.com/juju/juju/network"
	_ "github.com/juju/juju/provider/maas"
	"github.com/juju/juju/state"
	states "github.com/juju/juju/status"
	coretesting "github.com/juju/juju/testing"
	"github.com/juju/juju/testing/factory"
	"github.com/juju/juju/version"
	"github.com/juju/utils"
	semversion "github.com/juju/version"
	corecharm "gopkg.in/juju/charmrepo.v2-unstable"
	"gopkg.in/juju/names.v2"
	goyaml "gopkg.in/yaml.v1"
)

const (
	envDataDir      = "FAKE_JUJU_DATA_DIR"
	envLogsDir      = "FAKE_JUJU_LOGS_DIR"
	envFailuresFile = "FAKE_JUJU_FAILURES"
)

func main() {
	code := 0
	if len(os.Args) > 1 {
		err := handleCommand(os.Args[1])
		if err != nil {
			fmt.Println(err.Error())
			code = 1
		}
	} else {
		// This kicks off the daemon.  See FakeJujuSuite below.
		t := &testing.T{}
		coretesting.MgoTestPackage(t)
	}
	os.Exit(code)
}

func handleCommand(command string) error {
	filenames := newFakeJujuFilenames("", "", "")
	if command == "bootstrap" {
		return handleBootstrap(filenames)
	}
	if command == "show-controller" {
		return handleAPIInfo(filenames)
	}
	if command == "destroy-controller" {
		return handleDestroyController(filenames)
	}
	return errors.New("command not found")
}

func handleBootstrap(filenames fakejujuFilenames) (returnedErr error) {
	argc := len(os.Args)
	if argc < 4 {
		return errors.New(
			"error: controller name and cloud name are required")
	}
	if err := filenames.ensureDirsExist(); err != nil {
		return err
	}
	controllerName := os.Args[argc-1]

	// Start the fake-juju daemon.
	command := exec.Command(os.Args[0])
	command.Env = os.Environ()
	command.Env = append(
		command.Env, "ADMIN_PASSWORD="+"pwd")
	defaultSeries := "trusty"
	command.Env = append(command.Env, "DEFAULT_SERIES="+defaultSeries)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	command.Start()

	var whence string
	defer func() {
		if returnedErr != nil {
			if err := destroyController(filenames); err != nil {
				fmt.Printf("could not destroy controller when %s failed: %v\n", whence, err)
			}
			returnedErr = fmt.Errorf("bootstrap failed while %s: %v", whence, returnedErr)
		}
	}()

	// Get the internal info from the daemon and store it.
	result, err := parseApiInfo(stdout)
	if err != nil {
		whence = "parsing bootstrap result"
		return err
	}
	if err := result.copyConfig(os.Getenv("JUJU_DATA"), controllerName); err != nil {
		whence = "copying config"
		return err
	}
	if err := updateBootstrapResult(result); err != nil {
		whence = "updating bootstrap result"
		return err
	}
	if err := result.apply(filenames); err != nil {
		whence = "setup"
		return err
	}

	// Wait for the daemon to finish starting up.
	if err := waitForBootstrapCompletion(result); err != nil {
		whence = "waiting-for-ready"
		return err
	}

	return nil
}

func waitForBootstrapCompletion(result *bootstrapResult) error {
	apiInfo := result.apiInfo()
	dialOpts := api.DialOpts{
		DialAddressInterval: 50 * time.Millisecond,
		Timeout:             5 * time.Second,
		RetryDelay:          2 * time.Second,
	}
	state, err := api.Open(apiInfo, dialOpts)
	if err != nil {
		return err
	}
	client := state.Client()
	watcher, err := client.WatchAll()
	if err != nil {
		return err
	}
	deltas, err := watcher.Next()
	if err != nil {
		return err
	}
	for _, delta := range deltas {
		entityId := delta.Entity.EntityId()
		if entityId.Kind == "machine" {
			if entityId.Id == "0" {
				return nil
			}
		}
	}
	return errors.New("invalid delta")
}

func handleAPIInfo(filenames fakejujuFilenames) error {
	info, err := readProcessInfo(filenames)
	if err != nil {
		return err
	}

	jujuHome := os.Getenv("JUJU_DATA")
	osenv.SetJujuXDGDataHome(jujuHome)
	cmd := controller.NewShowControllerCommand()
	if err := coretesting.InitCommand(cmd, os.Args[2:]); err != nil {
		return err
	}
	ctx := coretesting.ContextForDir(nil, info.WorkDir)
	if err := cmd.Run(ctx); err != nil {
		return err
	}
	fmt.Print(ctx.Stdout)
	return nil
}

func handleDestroyController(filenames fakejujuFilenames) error {
	info, err := readProcessInfo(filenames)
	if err != nil {
		return err
	}
	filenames = newFakeJujuFilenames("", "", info.WorkDir)
	return destroyController(filenames)
}

func destroyController(filenames fakejujuFilenames) error {
	fd, err := os.OpenFile(filenames.fifoFile(), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer fd.Close()
	_, err = fd.WriteString("destroy\n")
	if err != nil {
		return err
	}
	return nil
}

// processInfo holds all the information that fake-juju uses internally.
type processInfo struct {
	WorkDir      string
	EndpointAddr string
	Uuid         string
	CACert       []byte
}

func readProcessInfo(filenames fakejujuFilenames) (*processInfo, error) {
	infoPath := filenames.infoFile()
	data, err := ioutil.ReadFile(infoPath)
	if err != nil {
		return nil, err
	}
	info := &processInfo{}
	err = goyaml.Unmarshal(data, info)
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (info processInfo) write(infoPath string) error {
	data, _ := goyaml.Marshal(&info)
	if err := ioutil.WriteFile(infoPath, data, 0644); err != nil {
		return err
	}
	return nil
}

// fakejujuFilenames encapsulates the paths to all the directories and
// files that are relevant to fake-juju.
type fakejujuFilenames struct {
	datadir string
	logsdir string
}

func newFakeJujuFilenames(datadir, logsdir, jujucfgdir string) fakejujuFilenames {
	if datadir == "" {
		datadir = os.Getenv(envDataDir)
		if datadir == "" {
			if jujucfgdir == "" {
				jujucfgdir = os.Getenv("JUJU_DATA")
			}
			datadir = jujucfgdir
		}
	}
	if logsdir == "" {
		logsdir = os.Getenv(envLogsDir)
		if logsdir == "" {
			logsdir = datadir
		}
	}
	return fakejujuFilenames{datadir, logsdir}
}

func (fj fakejujuFilenames) ensureDirsExist() error {
	if err := os.MkdirAll(fj.datadir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(fj.logsdir, 0755); err != nil {
		return err
	}
	return nil
}

// infoFile() returns the path to the file that fake-juju uses as
// its persistent storage for internal data.
func (fj fakejujuFilenames) infoFile() string {
	return filepath.Join(fj.datadir, "fakejuju")
}

// logsFile() returns the path to the file where fake-juju writes
// its logs.  Note that the normal Juju logs are not written here.
func (fj fakejujuFilenames) logsFile() string {
	return filepath.Join(fj.logsdir, "fake-juju.log")
}

// fifoFile() returns the path to the FIFO file used by fake-juju.
// The FIFO is used by the fake-juju subcommands to interact with
// the daemon.
func (fj fakejujuFilenames) fifoFile() string {
	return filepath.Join(fj.datadir, "fifo")
}

// caCertFile() returns the path to the file holding the CA certificate
// used by the Juju API server.  fake-juju writes the cert there as a
// convenience for users.  It is not actually used for anything.
func (fj fakejujuFilenames) caCertFile() string {
	return filepath.Join(fj.datadir, "cert.ca")
}

// bootstrapResult encapsulates all significant information that came
// from bootstrapping a controller.
type bootstrapResult struct {
	dummyControllerName string
	cfgdir              string
	uuid                string
	username            string
	password            string
	addresses           []string
	caCert              []byte
}

// apiInfo() composes the Juju API info corresponding to the result.
func (br bootstrapResult) apiInfo() *api.Info {
	return &api.Info{
		Addrs:    br.addresses,
		Tag:      names.NewUserTag(br.username),
		Password: br.password,
		CACert:   string(br.caCert),
		ModelTag: names.NewModelTag(br.uuid),
	}
}

// fakeJujuInfo() composes, from the result, the set of information
// that fake-juju should use internally.
func (br bootstrapResult) fakeJujuInfo() *processInfo {
	return &processInfo{
		WorkDir:      br.cfgdir,
		EndpointAddr: br.addresses[0],
		Uuid:         br.uuid,
		CACert:       br.caCert,
	}
}

// logsSymlinkFilenames() determines the source and target paths for
// a symlink to the fake-juju logs file.  Such a symlink is relevant
// because the fake-juju daemon may not know where the log file is
// meant to go. It defaults to putting the log file in the default Juju
// config dir. In that case, a symlink should be created from there to
// the user-defined Juju config dir ($JUJU_DATA).
func (br bootstrapResult) logsSymlinkFilenames(targetLogsFile string) (source, target string) {
	if os.Getenv(envLogsDir) != "" {
		return "", ""
	}

	filenames := newFakeJujuFilenames("", "", br.cfgdir)
	source = filenames.logsFile()
	target = targetLogsFile
	return source, target
}

// apply() writes out the information from the bootstrap result to the
// various files identified by the provided filenames.
func (br bootstrapResult) apply(filenames fakejujuFilenames) error {
	if err := br.fakeJujuInfo().write(filenames.infoFile()); err != nil {
		return err
	}

	logsSource, logsTarget := br.logsSymlinkFilenames(filenames.logsFile())
	if logsSource != "" && logsTarget != "" {
		if err := os.Symlink(logsSource, logsTarget); err != nil {
			return err
		}
	}

	if err := ioutil.WriteFile(filenames.caCertFile(), br.caCert, 0644); err != nil {
		return err
	}

	return nil
}

func (br bootstrapResult) copyConfig(targetCfgDir, controllerName string) error {
	for _, name := range []string{"controllers.yaml", "models.yaml", "accounts.yaml"} {
		source := filepath.Join(br.cfgdir, name)
		target := filepath.Join(targetCfgDir, name)

		input, err := ioutil.ReadFile(source)
		if err != nil {
			return err
		}
		// Generated configuration by test fixtures has the controller name
		// hard-coded to "kontroll". A simple replace should fix this for
		// clients using this config and expecting a specific controller
		// name.
		output := strings.Replace(string(input), dummyControllerName, controllerName, -1)
		err = ioutil.WriteFile(target, []byte(output), 0644)
		if err != nil {
			return err
		}
	}

	current := filepath.Join(targetCfgDir, "current-controller")
	if err := ioutil.WriteFile(current, []byte(controllerName), 0644); err != nil {
		return err
	}

	return nil
}

const dummyControllerName = jujutesting.ControllerName

func parseApiInfo(stdout io.ReadCloser) (*bootstrapResult, error) {
	buffer := bufio.NewReader(stdout)

	line, _, err := buffer.ReadLine()
	if err != nil {
		return nil, err
	}
	uuid := string(line)
	if !utils.IsValidUUIDString(uuid) {
		data, _ := ioutil.ReadAll(stdout)
		return nil, fmt.Errorf("%s\n%s", line, data)
	}

	line, _, err = buffer.ReadLine()
	if err != nil {
		return nil, err
	}
	workDir := string(line)

	result := &bootstrapResult{
		dummyControllerName: dummyControllerName,
		cfgdir:              workDir,
		uuid:                uuid,
	}
	return result, nil
}

func updateBootstrapResult(result *bootstrapResult) error {
	osenv.SetJujuXDGDataHome(result.cfgdir)
	store := jujuclient.NewFileClientStore()

	// hard-coded value in juju testing
	// This will be replaced in JUJU_DATA copy of the juju client config.
	currentController := result.dummyControllerName

	one, err := store.ControllerByName(currentController)
	if err != nil {
		return err
	}
	result.addresses = one.APIEndpoints
	result.caCert = []byte(one.CACert)

	accountDetails, err := store.AccountDetails(currentController)
	if err != nil {
		return err
	}
	result.username = accountDetails.User
	result.password = accountDetails.Password

	return nil
}

// Read the failures info file pointed by the FAKE_JUJU_FAILURES environment
// variable, if any. The format of the file is one entity name per line. If
// entity is found there, the code in FakeJujuSuite.TestStart will make that
// entity transition to an error state.
func readFailuresInfo() (map[string]bool, error) {
	log.Println("Checking for forced failures")
	failuresPath := os.Getenv(envFailuresFile)
	if failuresPath == "" {
		log.Printf("No %s env variable set\n", envFailuresFile)
	}
	log.Println("Reading failures file", failuresPath)
	failuresInfo := map[string]bool{}
	if _, err := os.Stat(failuresPath); os.IsNotExist(err) {
		log.Println("No failures file found")
		return failuresInfo, nil
	}
	file, err := os.Open(failuresPath)
	if err != nil {
		log.Println("Error opening failures file", err)
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var entity string
	for scanner.Scan() {
		entity = scanner.Text()
		log.Println("Add failure:", entity)
		failuresInfo[entity] = true
	}
	if err := scanner.Err(); err != nil {
		log.Println("Error reading failures file", err)
		return nil, err
	}
	return failuresInfo, nil
}

//===================================================================
// The fake-juju daemon (started by bootstrap) is found here.  It is
// implemented as a test suite.

type FakeJujuSuite struct {
	jujutesting.JujuConnSuite

	instanceCount  int
	machineStarted map[string]bool
	filenames      fakejujuFilenames
	logFile        *os.File
}

var _ = gc.Suite(&FakeJujuSuite{})

func (s *FakeJujuSuite) SetUpTest(c *gc.C) {
	var err error
	s.JujuConnSuite.SetUpTest(c)

	ports := s.APIState.APIHostPorts()
	err = s.State.SetAPIHostPorts(ports)
	c.Assert(err, gc.IsNil)

	s.machineStarted = make(map[string]bool)
	s.PatchValue(&corecharm.CacheDir, c.MkDir())
	defaultSeries := "trusty"
	if os.Getenv("DEFAULT_SERIES") != "" {
		defaultSeries = os.Getenv("DEFAULT_SERIES")
	}
	c.Assert(err, gc.IsNil)
	err = s.State.UpdateModelConfig(
		map[string]interface{}{"default-series": defaultSeries}, nil, nil)
	c.Assert(err, gc.IsNil)

	// Create a machine to manage the environment.
	stateServer := s.Factory.MakeMachine(c, &factory.MachineParams{
		InstanceId: s.newInstanceId(),
		Nonce:      agent.BootstrapNonce,
		Jobs:       []state.MachineJob{state.JobManageModel, state.JobHostUnits},
		Series:     defaultSeries,
	})
	currentVersion := version.Current.String()
	// XXX 2.0-beta version needs distro-arch in version number
	agentVersion, err := semversion.ParseBinary(currentVersion + "-xenial-amd64")
	c.Assert(err, gc.IsNil)
	c.Assert(stateServer.SetAgentVersion(agentVersion), gc.IsNil)
	address := network.NewScopedAddress("127.0.0.1", network.ScopeCloudLocal)
	c.Assert(stateServer.SetProviderAddresses(address), gc.IsNil)
	now := time.Now()
	sInfo := states.StatusInfo{
		Status:  states.Started,
		Message: "",
		Since:   &now,
	}
	c.Assert(stateServer.SetStatus(sInfo), gc.IsNil)
	_, err = stateServer.SetAgentPresence()
	c.Assert(err, gc.IsNil)
	s.State.StartSync()
	err = stateServer.WaitAgentPresence(coretesting.LongWait)
	c.Assert(err, gc.IsNil)

	apiInfo := s.APIInfo(c)
	//fmt.Println(apiInfo.Addrs[0])
	jujuHome := osenv.JujuXDGDataHome()

	binPath := filepath.Join(jujuHome, "bin")
	os.Mkdir(binPath, 0755)
	fakeSSHData := []byte("#!/bin/sh\nsleep 1\n")
	fakeSSHPath := filepath.Join(binPath, "ssh")
	err = ioutil.WriteFile(fakeSSHPath, fakeSSHData, 0755)
	c.Assert(err, gc.IsNil)
	os.Setenv("PATH", binPath+":"+os.Getenv("PATH"))

	s.filenames = newFakeJujuFilenames("", "", jujuHome)
	syscall.Mknod(s.filenames.fifoFile(), syscall.S_IFIFO|0666, 0)

	// Logging
	logPath := s.filenames.logsFile()
	s.logFile, err = os.OpenFile(logPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	c.Assert(err, gc.IsNil)

	log.SetOutput(s.logFile)
	log.Println("Started fake-juju at ", jujuHome)

	// Send the info back to the bootstrap command.
	// IMPORTANT: don't remove this logging because it's used by the
	// bootstrap command.
	fmt.Println(apiInfo.ModelTag.Id())
	fmt.Println(jujuHome)
}

func (s *FakeJujuSuite) TearDownTest(c *gc.C) {
	log.Println("Tearing down processes")
	s.JujuConnSuite.TearDownTest(c)
	log.Println("Closing log file")
	s.logFile.Close()
}

func (s *FakeJujuSuite) TestStart(c *gc.C) {
	fifoPath := s.filenames.fifoFile()
	watcher := s.State.Watch()
	go func() {
		log.Println("Open commands FIFO", fifoPath)
		fd, err := os.Open(fifoPath)
		if err != nil {
			log.Println("Failed to open commands FIFO")
		}
		c.Assert(err, gc.IsNil)
		defer func() {
			if err := fd.Close(); err != nil {
				c.Logf("failed closing FIFO file: %s", err)
			}
			// Mark the controller as destroyed by renaming some files.
			if err := os.Rename(fifoPath, fifoPath+".destroyed"); err != nil {
				c.Logf("failed renaming FIFO file: %s", err)
			}
			infofile := s.filenames.infoFile()
			if err := os.Rename(infofile, infofile+".destroyed"); err != nil {
				c.Logf("failed renaming info file: %s", err)
			}
		}()
		scanner := bufio.NewScanner(fd)
		log.Println("Listen for commands on FIFO", fifoPath)
		scanner.Scan()
		log.Println("Stopping fake-juju")
		watcher.Stop()
	}()
	for {
		log.Println("Watching deltas")
		deltas, err := watcher.Next()
		log.Println("Got deltas")
		if err != nil {
			if err.Error() == "watcher was stopped" {
				log.Println("Watcher stopped")
				break
			}
			log.Println("Unexpected error", err.Error())
		}
		c.Assert(err, gc.IsNil)
		for _, d := range deltas {

			entity, err := json.MarshalIndent(d.Entity, "", "  ")
			c.Assert(err, gc.IsNil)
			verb := "change"
			if d.Removed {
				verb = "remove"
			}
			log.Println("Processing delta", verb, d.Entity.EntityId().Kind, string(entity[:]))

			entityId := d.Entity.EntityId()
			if entityId.Kind == "machine" {
				machineId := entityId.Id
				c.Assert(s.handleAddMachine(machineId), gc.IsNil)
			}
			if entityId.Kind == "unit" {
				unitId := entityId.Id
				c.Assert(s.handleAddUnit(unitId), gc.IsNil)
			}
			log.Println("Done processing delta")
		}
	}
	log.Println("Stopping fake-juju")
}

func (s *FakeJujuSuite) handleAddMachine(id string) error {
	machine, err := s.State.Machine(id)
	log.Println("Handle machine", id)
	if err != nil {
		return err
	}
	if instanceId, _ := machine.InstanceId(); instanceId == "" {
		err = machine.SetProvisioned(s.newInstanceId(), agent.BootstrapNonce, nil)
		if err != nil {
			log.Println("Got error with SetProvisioned", err)
			return err
		}
		address := network.NewScopedAddress("127.0.0.1", network.ScopeCloudLocal)
		err = machine.SetProviderAddresses(address)
		if err != nil {
			log.Println("Got error with SetProviderAddresses", err)
			return err
		}
	}
	status, _ := machine.Status()
	log.Println("Machine has status:", string(status.Status), status.Message)
	if status.Status == states.Pending {
		if err = s.startMachine(machine); err != nil {
			log.Println("Got error with startMachine:", err)
			return err
		}
	} else if status.Status == states.Started {
		log.Println("Starting units on machine", id)
		if _, ok := s.machineStarted[id]; !ok {
			s.machineStarted[id] = true
			if err = s.startUnits(machine); err != nil {
				log.Println("Got error with startUnits", err)
				return err
			}
		}
	}
	return nil
}

func (s *FakeJujuSuite) handleAddUnit(id string) error {
	unit, err := s.State.Unit(id)
	log.Println("Handle unit", id)
	if err != nil {
		log.Println("Got error with get unit", err)
		return err
	}
	machineId, err := unit.AssignedMachineId()
	if err != nil {
		return nil
	}
	log.Println("Got machineId", machineId)
	machine, err := s.State.Machine(machineId)
	if err != nil {
		log.Println("Got error with unit AssignedMachineId", err)
		return err
	}
	machineStatus, _ := machine.Status()
	if machineStatus.Status != states.Started {
		return nil
	}
	status, _ := unit.Status()
	log.Println("Unit has status", string(status.Status), status.Message)
	if status.Status != states.Active && status.Status != states.Error {
		log.Println("Start unit", id)
		err = s.startUnit(unit)
		if err != nil {
			log.Println("Got error changing unit status", id, err)
			return err
		}
	} else if status.Status != states.Error {
		failuresInfo, err := readFailuresInfo()
		if err != nil {
			return err
		}
		if _, ok := failuresInfo["unit-"+id]; ok {
			agentStatus, err := unit.AgentStatus()
			if err != nil {
				log.Println("Got error checking agent status", id, err)
				return err
			}
			if agentStatus.Status != states.Error {
				log.Println("Error unit", id)
				err = s.errorUnit(unit)
				if err != nil {
					log.Println("Got error erroring unit status", id, err)
					return err
				}
			}
		}
	}
	return nil
}

func (s *FakeJujuSuite) startMachine(machine *state.Machine) error {
	time.Sleep(500 * time.Millisecond)
	now := time.Now()
	sInfo := states.StatusInfo{
		Status:  states.Started,
		Message: "",
		Since:   &now,
	}
	err := machine.SetStatus(sInfo)
	if err != nil {
		return err
	}
	currentVersion := version.Current.String()
	agentVersion, err := semversion.ParseBinary(currentVersion + "-xenial-amd64")
	if err != nil {
		return err
	}
	err = machine.SetAgentVersion(agentVersion)
	if err != nil {
		return err
	}
	_, err = machine.SetAgentPresence()
	if err != nil {
		return err
	}
	s.State.StartSync()
	err = machine.WaitAgentPresence(coretesting.LongWait)
	if err != nil {
		return err
	}
	return nil
}

func (s *FakeJujuSuite) errorMachine(machine *state.Machine) error {
	time.Sleep(500 * time.Millisecond)
	now := time.Now()
	sInfo := states.StatusInfo{
		Status:  states.Error,
		Message: "machine errored",
		Since:   &now,
	}
	err := machine.SetStatus(sInfo)
	if err != nil {
		return err
	}
	return nil
}

func (s *FakeJujuSuite) startUnits(machine *state.Machine) error {
	units, err := machine.Units()
	if err != nil {
		return err
	}
	return nil
	for _, unit := range units {
		unitStatus, _ := unit.Status()
		if unitStatus.Status != states.Active {
			if err = s.startUnit(unit); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *FakeJujuSuite) startUnit(unit *state.Unit) error {
	now := time.Now()
	sInfo := states.StatusInfo{
		Status:  states.Started,
		Message: "",
		Since:   &now,
	}
	err := unit.SetStatus(sInfo)
	if err != nil {
		return err
	}
	_, err = unit.SetAgentPresence()
	if err != nil {
		return err
	}
	s.State.StartSync()
	err = unit.WaitAgentPresence(coretesting.LongWait)
	if err != nil {
		return err
	}
	idleInfo := states.StatusInfo{
		Status:  states.Idle,
		Message: "",
		Since:   &now,
	}
	err = unit.SetAgentStatus(idleInfo)
	if err != nil {
		return err
	}
	return nil
}

func (s *FakeJujuSuite) errorUnit(unit *state.Unit) error {
	log.Println("Erroring unit", unit.Name())
	now := time.Now()
	sInfo := states.StatusInfo{
		Status:  states.Idle,
		Message: "unit errored",
		Since:   &now,
	}
	err := unit.SetAgentStatus(sInfo)
	if err != nil {
		return err
	}
	log.Println("Done eroring unit", unit.Name())
	return nil
}

func (s *FakeJujuSuite) newInstanceId() instance.Id {
	s.instanceCount += 1
	return instance.Id(fmt.Sprintf("id-%d", s.instanceCount))
}
