package main

import (
	"context"
	"flag"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestFixedModeBindFailureReturnsWithoutWaitingForUncancelledWorkers(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestFixedModeBindFailureHelper$")
	cmd.Env = append(os.Environ(), "HIFI_BUSY_PORT_HELPER=1", "HIFI_BUSY_PORT="+listener.Addr().String(), "HIFI_BUSY_PORT_HOME="+t.TempDir())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed startup did not return cleanly: %v\n%s", err, output)
	}
}

func TestFixedModeBindFailureHelper(t *testing.T) {
	if os.Getenv("HIFI_BUSY_PORT_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	flag.CommandLine = flag.NewFlagSet("manager-startup", flag.ContinueOnError)
	os.Args = []string{"hifi-manager", "-listen", os.Getenv("HIFI_BUSY_PORT"), "-home", os.Getenv("HIFI_BUSY_PORT_HOME")}
	if err := run(); err == nil {
		t.Fatal("occupied port was accepted")
	}
}
