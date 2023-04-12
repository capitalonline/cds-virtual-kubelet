package cdsapi

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
)

func Run(name string, arg ...string) (string, error) {
	var stdoutBuf, stderrBuf bytes.Buffer
	var err error
	if _, err = exec.LookPath(name); err != nil {
		return "", err
	}
	cmd := exec.Command(name, arg...)
	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()
	stdout := io.MultiWriter(os.Stdout, &stdoutBuf)
	stderr := io.MultiWriter(os.Stderr, &stderrBuf)
	if _, err = fmt.Fprintf(os.Stdout, "oscmd: %s\n", cmd.String()); err != nil {
		return "", err
	}
	if err = cmd.Start(); err != nil {
		return "", err
	}
	go func() {
		_, _ = io.Copy(stdout, stdoutPipe)
	}()
	go func() {
		_, _ = io.Copy(stderr, stderrPipe)
	}()
	if err = cmd.Wait(); err != nil {
		return "", err
	}
	return stdoutBuf.String(), nil
}
