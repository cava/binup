package main

import (
	"fmt"
	"io"
	"os/exec"
)

type xzReadCloser struct {
	r   io.ReadCloser
	cmd *exec.Cmd
}

func (x *xzReadCloser) Read(p []byte) (int, error) { return x.r.Read(p) }

func (x *xzReadCloser) Close() error {
	err1 := x.r.Close()
	err2 := x.cmd.Wait()
	if err1 != nil {
		return err1
	}
	return err2
}

func openXZ(path string) (io.ReadCloser, error) {
	if _, err := exec.LookPath("xz"); err != nil {
		return nil, fmt.Errorf("xz decompression needs the `xz` binary on PATH (Go stdlib has no xz reader)")
	}
	cmd := exec.Command("xz", "-dc", path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &xzReadCloser{r: out, cmd: cmd}, nil
}
