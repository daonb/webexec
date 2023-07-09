//go:generate go run git.rootprojects.org/root/go-gitver/v2 --fail
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kardianos/osext"
	"github.com/pion/webrtc/v3"
	"github.com/tuzig/webexec/httpserver"
	"github.com/tuzig/webexec/peers"
	"github.com/tuzig/webexec/pidfile"
	"github.com/urfave/cli/v2"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/crypto/ssh/terminal"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	// Logger is our global logger
	Logger *zap.SugaredLogger
	// generated by go-gitver
	commit  = "0000000"
	version = "UNRELEASED"
	date    = "0000-00-00T00:00:00+0000"
	// ErrAgentNotRunning is returned by commands that require a running agent
	ErrAgentNotRunning = errors.New("agent is not running")
	gotExitSignal      chan bool
	logWriter          io.Writer
	key                *KeyType
)

// PIDFIlePath return the path of the PID file
func PIDFilePath() string {
	return RunPath("webexec.pid")
}

// InitAgentLogger intializes an agent logger and sets the global Logger
func InitAgentLogger() *zap.SugaredLogger {
	// rotate the log file
	logWriter = &lumberjack.Logger{
		Filename:   Conf.logFilePath,
		MaxSize:    10, // megabytes
		MaxBackups: 3,
		MaxAge:     28, // days
	}
	w := zapcore.AddSync(logWriter)

	// TODO: use pion's logging
	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(zapcore.EncoderConfig{
			MessageKey:  "webexec",
			LevelKey:    "level",
			EncodeLevel: zapcore.CapitalLevelEncoder,
			TimeKey:     "time",
			EncodeTime:  zapcore.ISO8601TimeEncoder,
		}),
		w,
		Conf.logLevel,
	)
	logger := zap.New(core)
	defer logger.Sync()
	Logger = logger.Sugar()
	// redirect stderr
	e, _ := os.OpenFile(
		Conf.errFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	Dup2(int(e.Fd()), 2)
	return Logger
}

// InitDevLogger starts a logger for development
func InitDevLogger() *zap.SugaredLogger {
	zapConf := []byte(`{
		  "level": "debug",
		  "encoding": "console",
		  "outputPaths": ["stdout"],
		  "errorOutputPaths": ["stderr"],
		  "encoderConfig": {
		    "messageKey": "message",
		    "levelKey": "level",
		    "levelEncoder": "lowercase"
		  }
		}`)

	var cfg zap.Config
	if err := json.Unmarshal(zapConf, &cfg); err != nil {
		panic(err)
	}
	l, err := cfg.Build()
	Logger = l.Sugar()
	if err != nil {
		panic(err)
	}
	defer Logger.Sync()
	return Logger
}

// versionCMD prints version information
func versionCMD(c *cli.Context) error {
	fmt.Printf("Version: %s\n", version)
	fmt.Printf("Git Commit Hash: %s\n", commit)
	fmt.Printf("Build Date: %s\n", date)
	return nil
}

// stop - stops the agent
func stop(c *cli.Context) error {
	certs, err := GetCerts()
	if err != nil {
		return fmt.Errorf("Failed to load certificates: %s", err)
	}
	_, _, err = LoadConf(certs)
	if err != nil {
		return err
	}
	pidf, err := pidfile.Open(PIDFilePath())
	if os.IsNotExist(err) {
		return ErrAgentNotRunning
	}
	if err != nil {
		return err
	}
	if !pidf.Running() {
		return ErrAgentNotRunning
	}
	pid, err := pidf.Read()
	if err != nil {
		return fmt.Errorf("Failed to read the pidfile: %s", err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("Failed to find the agetnt's process: %s", err)
	}
	fmt.Printf("Sending a SIGINT to agent process %d\n", pid)
	err = process.Signal(syscall.SIGINT)
	return err
}

// createPIDFile creates the pid file or returns an error if it exists
func createPIDFile() error {
	_, err := pidfile.New(PIDFilePath())
	if err == pidfile.ErrProcessRunning {
		return fmt.Errorf("agent is already running, doing nothing")
	}
	if err != nil {
		return fmt.Errorf("pid file creation failed: %q", err)
	}
	return nil
}

func forkAgent(address httpserver.AddressType) (int, error) {
	pidf, err := pidfile.Open(PIDFilePath())
	if pidf != nil && !os.IsNotExist(err) && pidf.Running() {
		fmt.Println("agent is already running, doing nothing")
		return 0, nil
	}
	// start the agent process and exit
	execPath, err := osext.Executable()
	if err != nil {
		return 0, fmt.Errorf("Failed to find the executable: %s", err)
	}
	cmd := exec.Command("bash", "-c",
		fmt.Sprintf("%s start --agent --address %s >> %s",
			execPath, string(address), Conf.logFilePath))
	cmd.Env = nil
	err = cmd.Start()
	if err != nil {
		return 0, fmt.Errorf("agent failed to start :%q", err)
	}
	time.Sleep(100 * time.Millisecond)
	return cmd.Process.Pid, nil
}

// start - start the user's agent
func start(c *cli.Context) error {
	certs, err := GetCerts()
	if err != nil {
		return fmt.Errorf("Failed to get the certificates: %s", err)
	}
	_, address, err := LoadConf(certs)
	if err != nil {
		return err
	}
	if c.IsSet("address") {
		address = httpserver.AddressType(c.String("address"))
	}
	// TODO: do we need this?
	peers.PtyMux = peers.PtyMuxType{}
	debug := c.Bool("debug")
	var loggerOption fx.Option
	if debug {
		loggerOption = fx.Provide(InitDevLogger)
		err := createPIDFile()
		if err != nil {
			return err
		}
	} else {
		if !c.Bool("agent") {
			pid, err := forkAgent(address)
			if err != nil {
				return err
			}
			fmt.Printf("agent started as process #%d\n", pid)
			return nil
		} else {
			loggerOption = fx.Provide(InitAgentLogger)
			err := createPIDFile()
			if err != nil {
				return err
			}
		}
	}
	// the code below runs for both --debug and --agent
	sigChan := make(chan os.Signal, 1)
	app := fx.New(
		loggerOption,
		fx.Supply(""),
		fx.Provide(
			LoadConf,
			httpserver.NewConnectHandler,
			// TODO: find a way to pass the filepath
			fx.Annotate(NewFileAuth, fx.As(new(httpserver.AuthBackend))),
			NewSockServer,
			NewPeerbookClient,
			GetCerts,
		),
		fx.Invoke(httpserver.StartHTTPServer, StartSocketServer, StartPeerbookClient),
	)
	if debug {
		app.Run()
		return nil
	} else {
		err = app.Start(context.Background())
	}
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	Logger.Infof("Shutting down")
	os.Remove(PIDFilePath())
	return nil
}

/* TBD:
func paste(c *cli.Context) error {
	fmt.Println("Soon, we'll be pasting data from the clipboard to STDOUT")
	return nil
}
func copyCMD(c *cli.Context) error {
	fmt.Println("Soon, we'll be copying data from STDIN to the clipboard")
	return nil
*/
// restart function restarts the agent or starts it if it is stopped
func restart(c *cli.Context) error {
	err := stop(c)
	if err != nil && err != ErrAgentNotRunning {
		return err
	}
	// wait for the process to stop
	// TODO: https://github.com/tuzig/webexec/issues/18
	time.Sleep(1 * time.Second)
	return start(c)
}

// accept function accepts offers to connect
func accept(c *cli.Context) error {
	var id string
	certs, err := GetCerts()
	if err != nil {
		return err
	}
	_, _, err = LoadConf(certs)
	if err != nil {
		return err
	}
	fp := GetSockFP()
	pid, err := getAgentPid()
	if err != nil {
		return err
	}
	if pid == 0 {
		// start the agent
		var address httpserver.AddressType
		if c.IsSet("address") {
			address = httpserver.AddressType(c.String("address"))
		} else {
			address = defaultHTTPServer
		}
		_, err = forkAgent(address)
		if err != nil {
			return fmt.Errorf("Failed to fork agent: %s", err)
		}
	}
	httpc := http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", fp)
			},
		},
	}
	// First get the agent's status and print to the clist
	var msg string
	var r *http.Response
	for i := 0; i < 30; i++ {
		r, err = httpc.Get("http://unix/status")
		if err == nil {
			goto gotstatus
		}
		time.Sleep(100 * time.Millisecond)
	}
	msg = "Failed to communicate with agent"
	fmt.Println(msg)
	return fmt.Errorf(msg)
gotstatus:
	defer r.Body.Close()
	body, _ := ioutil.ReadAll(r.Body)
	if r.StatusCode == http.StatusNoContent {
		return fmt.Errorf("didn't get a status from the agent")
	} else if r.StatusCode != http.StatusOK {
		return fmt.Errorf("agent's socket GET status return: %d", r.StatusCode)
	}
	fmt.Println(string(body))

	can := []byte{}
	for {
		line, err := terminal.ReadPassword(0)
		if err != nil {
			fmt.Printf("ReadPassword error: %s", err)
			return err
		}
		can = append(can, line...)
		var js json.RawMessage
		// If it's not the end of a candidate, continue reading
		if len(line) == 0 || line[len(line)-1] != '}' || json.Unmarshal(can, &js) != nil {
			continue
		}
		if id == "" {
			resp, err := httpc.Post(
				"http://unix/offer/", "application/json", bytes.NewBuffer(can))
			if err != nil {
				return fmt.Errorf("Failed to POST agent's unix socket: %s", err)
			}
			if resp.StatusCode != http.StatusOK {
				msg, _ := ioutil.ReadAll(resp.Body)
				defer resp.Body.Close()
				return fmt.Errorf("Agent returned an error: %s", msg)
			}
			var body map[string]string
			json.NewDecoder(resp.Body).Decode(&body)
			defer resp.Body.Close()
			id = body["id"]
			delete(body, "id")
			msg, err := json.Marshal(body)
			fmt.Println(string(msg))
			go func() {
				for {
					can, err := getCandidate(httpc, id)
					if err != nil {
						return
					}
					if len(can) > 0 {
						fmt.Println(can)
					} else {
						os.Exit(0)
					}
				}
			}()
		} else {
			req, err := http.NewRequest("PUT", "http://unix/offer/"+id, bytes.NewReader(can))
			if err != nil {
				return fmt.Errorf("Failed to create new PUT request: %q", err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := httpc.Do(req)
			if err != nil {
				return fmt.Errorf("Failed to PUT candidate: %q", err)
			}
			if resp.StatusCode != http.StatusOK {
				// msg, _ := ioutil.ReadAll(resp.Body)
				// defer resp.Body.Close()
				return fmt.Errorf("Got a server error when PUTing: %v", resp.StatusCode)
			}
		}
		can = []byte{}
	}
	return nil
}
func getCandidate(httpc http.Client, id string) (string, error) {
	r, err := httpc.Get("http://unix/offer/" + id)
	if err != nil {
		return "", fmt.Errorf("Failed to get candidate from the unix socket: %s", err)
	}
	body, _ := ioutil.ReadAll(r.Body)
	defer r.Body.Close()
	if r.StatusCode == http.StatusNoContent {
		return "", nil
	} else if r.StatusCode != http.StatusOK {
		return "", fmt.Errorf("agent's socker return status: %d", r.StatusCode)
	}
	return string(body), nil
}

// status function prints the status of the agent
func getAgentPid() (int, error) {
	fp := PIDFilePath()
	pidf, err := pidfile.Open(fp)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !pidf.Running() {
		os.Remove(fp)
		return 0, nil
	}
	return pidf.Read()
}
func status(c *cli.Context) error {
	pid, err := getAgentPid()
	if err != nil {
		return err
	}
	if pid == 0 {
		fmt.Println("Agent is not running")
	} else {
		fmt.Printf("Agent is running with process id %d\n", pid)
	}
	// TODO: Get the the fingerprints of connected peers from the agent using the status socket
	fp := getFP()
	if fp == "" {
		fmt.Println("Unitialized, please run `webexec init`")
	} else {
		fmt.Printf("Fingerprint:  %s\n", fp)
	}
	return nil
}
func initCMD(c *cli.Context) error {
	// init the dev logger so log messages are printed on the console
	InitDevLogger()
	homePath := ConfPath("")
	_, err := os.Stat(homePath)
	if os.IsNotExist(err) {
		err = os.MkdirAll(homePath, 0755)
		if err != nil {
			return err
		}
		fmt.Printf("Created %q directory\n", homePath)
	} else {
		return fmt.Errorf("%q already exists, leaving as is.", homePath)
	}
	fPath := ConfPath("certnkey.pem")
	key = &KeyType{Name: fPath}
	cert, err := key.generate()
	if err != nil {
		return cli.Exit(fmt.Sprintf("Failed to create certificate: %s", err), 2)
	}
	key.save(cert)
	// TODO: add a CLI option to make it !sillent
	fmt.Printf("Created certificate in: %s\n", fPath)
	uid := os.Getenv("PEERBOOK_UID")
	pbHost := os.Getenv("PEERBOOK_HOST")
	name := os.Getenv("PEERBOOK_NAME")
	if name == "" {
		dn, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("Failed to get hostname: %s", err)
		}
		// let the user edit the host name
		fmt.Printf("Enter a name for this host [%s]: ", dn)
		reader := bufio.NewReader(os.Stdin)
		text, _ := reader.ReadString('\n')
		text = strings.TrimSpace(text)
		if text != "" {
			name = text
		} else {
			name = dn
		}
	}
	confPath, err := createConf(uid, pbHost, name)
	fmt.Printf("Created dotfile in: %s\n", confPath)
	if err != nil {
		return err
	}
	_, _, err = LoadConf([]webrtc.Certificate{*cert})
	if err != nil {
		return cli.Exit(fmt.Sprintf("Failed to parse default conf: %s", err), 1)
	}
	fp := getFP()
	fmt.Printf("Fingerprint:  %s\n", fp)
	if uid != "" {
		verified, err := verifyPeer(Conf.peerbookHost)
		if err != nil {
			return fmt.Errorf("Got an error verifying peer: %s", err)
		}
		if verified {
			fmt.Println("Verified by peerbook")
		} else {
			fmt.Println("Unverified by peerbook. Please use terminal7 to verify the fingerprint")
		}
	}
	return nil
}
func main() {
	app := &cli.App{
		Name:        "webexec",
		Usage:       "execute commands and pipe their stdin&stdout over webrtc",
		HideVersion: true,
		Commands: []*cli.Command{
			/* TODO: Add clipboard commands
			{
				Name:   "copy",
				Usage:  "Copy data from STDIN to the clipboard",
				Action: copyCMD,
			}, {
				Name:   "paste",
				Usage:  "Paste data from the clipboard to STDOUT",
				Action: paste,
			},*/
			{
				Name:   "version",
				Usage:  "Print version information",
				Action: versionCMD,
			}, {
				Name:  "restart",
				Usage: "restarts the agent",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "address",
						Aliases: []string{"a"},
						Usage:   "The address to listen to",
						Value:   "0.0.0.0:7777",
					},
				},
				Action: restart,
			}, {
				Name:    "start",
				Aliases: []string{"l"},
				Usage:   "Spawns a backgroung http server & webrtc peer",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "address",
						Aliases: []string{"a"},
						Usage:   "The address to listen to",
						Value:   "",
					},
					&cli.BoolFlag{
						Name:  "debug",
						Usage: "Run in debug mode in the foreground",
					},
					&cli.BoolFlag{
						Name:  "agent",
						Usage: "Run as agent, in the background",
					},
				},
				Action: start,
			}, {
				Name:   "status",
				Usage:  "webexec agent's status",
				Action: status,
			}, {
				Name:   "stop",
				Usage:  "stop the user's agent",
				Action: stop,
			}, {
				Name:   "init",
				Usage:  "initialize the conf file",
				Action: initCMD,
			}, {
				Name:   "accept",
				Usage:  "accepts an offer to connect",
				Action: accept,
			},
		},
	}
	err := app.Run(os.Args)
	if err != nil {
		fmt.Println(err)
	}
}
