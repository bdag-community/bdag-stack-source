package testutils

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/rpc"
)

// the configuration of the node
type nodeConfig struct {
	program     string
	listen      string
	rpclisten   string
	evmlisten   string
	evmWSlisten string
	evmPort     string
	rpcuser     string
	rpcpass     string
	homeDir     string
	dataDir     string
	logDir      string
	keyFile     string
	certFile    string
	extraArgs   []string
}

// args return the arguments list build from the nodeConfig
// which be used to launch the Blockdag node
func (n *nodeConfig) args() []string {
	args := []string{}
	if n.listen != "" {
		args = append(args, fmt.Sprintf("--listen=%s", n.listen))
	}
	if n.rpclisten != "" {
		args = append(args, fmt.Sprintf("--rpclisten=%s", n.rpclisten))
	}
	if n.evmlisten != "" {
		args = append(args, fmt.Sprintf(`--evmenv="--http --http.port=%s --ws --ws.port=%s"`, n.evmlisten, n.evmWSlisten))
	}
	if n.rpcuser != "" {
		args = append(args, fmt.Sprintf("--rpcuser=%s", n.rpcuser))
	}
	if n.rpcpass != "" {
		args = append(args, fmt.Sprintf("--rpcpass=%s", n.rpcpass))
	}
	if n.homeDir != "" {
		args = append(args, fmt.Sprintf("--appdata=%s", n.homeDir))
	}
	if n.dataDir != "" {
		args = append(args, fmt.Sprintf("--datadir=%s", n.dataDir))
	}
	if n.logDir != "" {
		args = append(args, fmt.Sprintf("--logdir=%s", n.logDir))
	}
	args = append(args, n.extraArgs...)
	return args
}

// node is the wrapper of a Blockdag node process. which contains all necessary
// configure information required to manage a Blockdag full node process.
type node struct {
	t      *testing.T
	config *nodeConfig
	id     string
	cmd    *exec.Cmd
	pid    int
	wg     sync.WaitGroup
}

func (n *node) Id() string {
	return n.id
}

// create an new node instance
func newNode(t *testing.T, config *nodeConfig) (*node, error) {
	// Validate the program and arguments to prevent command injection
	if err := validateProgramPath(config.program); err != nil {
		return nil, fmt.Errorf("invalid program path: %w", err)
	}
	if err := validateArgs(config.args()); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	// Test if home directory exists
	if _, err := os.Stat(config.homeDir); os.IsNotExist(err) {
		return nil, err
	}

	// Create RPC cert and key file
	if err := rpc.GenCertPair(config.certFile, config.keyFile); err != nil {
		return nil, err
	}

	return &node{
		t:      t,
		config: config,
		id:     filepath.Base(config.homeDir),
		cmd:    exec.Command(config.program, config.args()...),
	}, nil
}
func validateProgramPath(program string) error {
	if program == "" {
		return fmt.Errorf("program path is empty")
	}

	if _, err := exec.LookPath(program); err != nil {
		return fmt.Errorf("program not found: %w", err)
	}

	return nil
}

func validateArgs(args []string) error {
	for _, arg := range args {
		if arg == "" {
			return fmt.Errorf("empty argument detected")
		}

	}
	return nil
}

func (n *node) redirectOutput(reader io.ReadCloser, waitPid *sync.WaitGroup) error {
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		waitPid.Wait() //wait for pid is available
		r := bufio.NewReader(reader)
		for {
			l, err := r.ReadString('\n')
			if err != nil || err == io.EOF {
				return
			}
			n.t.Logf("%s: %s", n.id, l)
		}
	}()
	return nil
}
func (n *node) storePid(waitPid *sync.WaitGroup) error {
	n.pid = n.cmd.Process.Pid
	defer waitPid.Done()
	f, err := os.Create(filepath.Join(n.config.homeDir, ".pid"))
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(f, "%d\n", n.cmd.Process.Pid); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return nil
}

// start up the node instance which works as the wrapped Blockdag process
func (n *node) start() error {
	var waitpid sync.WaitGroup
	waitpid.Add(1)
	// redirect stdout
	if stdout, err := n.cmd.StdoutPipe(); err != nil {
		return err
	} else {
		n.redirectOutput(stdout, &waitpid)
	}
	// redirect stderr
	if stderr, err := n.cmd.StderrPipe(); err != nil {
		return err
	} else {
		n.redirectOutput(stderr, &waitpid)
	}
	// Launch command
	n.t.Logf("start node from %v", n.config.homeDir)
	if err := n.cmd.Start(); err != nil {
		return err
	}
	if err := n.storePid(&waitpid); err != nil {
		return err
	}
	return nil
}

func (n *node) stop() error {
	n.t.Logf("stop node from %v", n.config.homeDir)
	// check if process has not been started
	if n.pid == 0 {
		return nil
	}
	// need to check if process has been started
	if n.cmd.Process == nil {
		return nil
	}

	if err := n.cmd.Process.Signal(os.Interrupt); err != nil {
		// only log the signal error, and continue
		n.t.Logf("stop node [%s] got process signal error: %v", n.id, err)
	}

	// wait for stdout/stderr redirect pipe done
	n.wg.Wait()

	// wait for cmd done
	if err := n.cmd.Wait(); err != nil {
		// only log the cmd wait error and continue
		n.t.Logf("stop node [%s] got cmd wait error: %v", n.id, err)
	}

	return nil
}
