// Copyright 2015 Lorenz Leutgeb <lorenz.leutgeb@cod.uno>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// +build darwin dragonfly freebsd linux netbsd openbsd solaris

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

        "github.com/docker/go-plugins-helpers/volume"
)

// Socket address by convention. Docker will look there, so
// this needs to be in sync with upstream.
const socketAddress = "/run/docker/plugins/gcs.sock"

var (
	errDaemonDirty   = errors.New("gcsfuse did not exit cleanly")
	errUnknownVolume = errors.New("unknwon volume, no gcfsfuse instance found")
	errZombie        = errors.New("found gcfsfuse instance where there should be none")

)

type errBadRead struct {
	cause error
}

func (e errBadRead) Error() string {
	return fmt.Sprintf("failed to read from gcfsfuse, caused by: %s", e.cause.Error())
}

type errUnexpectedOutput struct {
	output string
}

func (e errUnexpectedOutput) Error() string {
	return fmt.Sprintf("unexpected output from gcfsfuse: %s", e.output)
}


// driver wraps multiple gcsfuse processes
type driver struct {
	*sync.Mutex

	// Maps bucket to the gcfsfuse command that owns the bucket.
	cmds map[string]exec.Cmd
}

var root = os.Args[len(os.Args)-1]

func init() {
	if _, err := exec.LookPath("gcsfuse"); err != nil {
		log.Fatal("Could not find gcsfuse.")
	}
	log.SetFlags(log.Lmicroseconds)
}

func main() {
	d := driver{
		Mutex: new(sync.Mutex),
		cmds:  make(map[string]exec.Cmd),
	}

	h := volume.NewHandler(d)
	log.Printf("Listening on %s with mount target %s\n", socketAddress, root)
	log.Println(h.ServeUnix(socketAddress, 0))
}

func (d driver) Mount(r *volume.MountRequest) (*volume.MountResponse, error) {
	d.Lock()
	defer d.Unlock()

	b := d.bucket(r.Name)

	daemon, ok := d.cmds[b]

	if ok {
		if daemon.ProcessState != nil && daemon.ProcessState.Exited() {
			return nil, errZombie
		}
		return &volume.MountResponse{Mountpoint: d.mountpoint(r.Name)}, nil
	}

	mnt := d.mountpoint(b)

	if err := os.MkdirAll(mnt, os.ModeTemporary); err != nil {
		return nil, err
	}

	daemon = *exec.Command("gcsfuse", append(os.Args[1:len(os.Args)-1], b, mnt)...)
	daemon.Stdout = os.Stdout
	rc, err := daemon.StderrPipe()
	if err != nil {
		return nil, errBadRead{err}
	}

	if err := daemon.Start(); err != nil {
		return nil, err
	}

	l, err := bufio.NewReader(io.TeeReader(rc, os.Stderr)).ReadString(byte('\n'))
	if err != nil {
		return nil, errBadRead{err}
	}

	if !strings.HasSuffix(l, "File system has been successfully mounted.\n") {
		return nil, errUnexpectedOutput{output: l}
	}

	d.cmds[b] = daemon

	go io.Copy(os.Stderr, rc)

	return &volume.MountResponse{Mountpoint: d.mountpoint(r.Name)}, nil
}

func (d driver) Remove(r *volume.RemoveRequest) error {
	d.Lock()
	defer d.Unlock()

	b := d.bucket(r.Name)

	daemon, ok := d.cmds[b]

	if !ok {
		log.Printf("Doing nothing when asked to remove volume for %s ...", r.Name)
		return nil
	}

	log.Printf("Interrupting gcsfuse %s", b)
	daemon.Process.Signal(os.Interrupt)
	ps, err := daemon.Process.Wait()
	if err != nil {
		log.Printf("Waiting for gcsfuse %s errored, returning error.", b)
		return err
	}
	if !ps.Success() {
		log.Printf("gcsfuse %s exited dirty, returning error.", b)
		return errDaemonDirty
	}

	return nil
}

func (d driver) Get(r *volume.GetRequest) (*volume.GetResponse, error) {
	d.Lock()
	defer d.Unlock()

	return &volume.GetResponse{
		Volume: &volume.Volume{
			Name: r.Name,
			Mountpoint: d.mountpoint(r.Name),
		},
	}, nil
}

func (d driver) List() (*volume.ListResponse, error) {
	d.Lock()
	defer d.Unlock()

	var volumes []*volume.Volume
	files, err := ioutil.ReadDir(root)

	if err != nil {
		return nil, err
	}

	for _, entry := range files {
		if entry.IsDir() {
			volumes = append(volumes, &volume.Volume{Name: entry.Name(), Mountpoint: d.mountpoint(entry.Name())})
		}
	}

	return &volume.ListResponse{Volumes: volumes}, nil
}

func (d driver) Path(r *volume.PathRequest) (*volume.PathResponse, error) {
	return &volume.PathResponse{Mountpoint: d.mountpoint(r.Name)}, nil
}

func (d driver) Create(r *volume.CreateRequest) error {
	return nil
}

func (d driver) Unmount(r *volume.UnmountRequest) error {
	return nil
}

func (d driver) mountpoint(name string) string {
	return filepath.Join(root, name)
}

func (d driver) bucket(name string) string {
	i := strings.Index(name, "/")
	if i == -1 {
		return name
	}
	return name[0:i]
}

func (d driver) Capabilities() *volume.CapabilitiesResponse {
	return &volume.CapabilitiesResponse{
		Capabilities: volume.Capability{Scope: "global"},
	}
}
