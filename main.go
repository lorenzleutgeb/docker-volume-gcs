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
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	plugin "github.com/calavera/dkvolume"
)

const (
	// Short name of this plugin.
	id = "gcs"

	// Socket address by convention. Docker will look there, so
	// this needs to be in sync with upstream.
	socketAddress = "/run/docker/plugins/gcs.sock"
)

var (
	// The path where volume plugins usually put their files.
	defaultRoot = filepath.Join(plugin.DefaultDockerRootDirectory, id)

	// Each mounted bucket gets it's own subdirectory below root.
	root = flag.String("root", defaultRoot, "Root directory for all mountpoints of this plugin.")

	// Credentials for Google Cloud as JSON Web Token. Primary use case are Service Accounts.
	// See https://developers.google.com/console/help/new/#serviceaccounts
	jwt = flag.String("jwt", "", "JSON Web Token to use for authentication")

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

// daemon is a gcsfuse process that owns a bucket.
type daemon struct {
	*exec.Cmd
	refs int
}

// driver wraps multiple daemons, all spawned off the same jwt and root
type driver struct {
	*sync.Mutex
	jwt, root string

	// Maps bucket to the daemon that owns the bucket.
	daemons map[string]*daemon
}

func init() {
	if _, err := exec.LookPath("gcsfuse"); err != nil {
		panic(err)
	}
}

func main() {
	flag.Parse()
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n", os.Args[0])
		flag.PrintDefaults()
	}

	d := driver{
		Mutex:   new(sync.Mutex),
		jwt:     *jwt,
		root:    *root,
		daemons: make(map[string]*daemon),
	}

	h := plugin.NewHandler(d)
	log.Printf("Listening on %s\n", socketAddress)
	log.Println(h.ServeUnix("root", socketAddress))
}

func (d driver) Create(r plugin.Request) plugin.Response {
	d.Lock()
	defer d.Unlock()

	dmn := d.daemons[r.Name]

	if dmn != nil {
		// There's a daemon that was never called, we're set!
		if dmn.ProcessState == nil || dmn.refs > 0 {
			return plugin.Response{}
		}
		return bail(errZombie)
	}

	mnt := d.mountpoint(r.Name)

	if err := os.MkdirAll(mnt, os.ModeTemporary); err != nil {
		return bail(err)
	}

	var args []string
	if d.jwt != "" {
		args = []string{"--key-file", d.jwt}
	}

	cmd := exec.Command("gcsfuse", append(args, r.Name, mnt)...)

	d.daemons[r.Name] = &daemon{cmd, 0}

	return plugin.Response{}
}

func (d driver) Mount(r plugin.Request) plugin.Response {
	d.Lock()
	defer d.Unlock()

	daemon := d.daemons[r.Name]

	if daemon == nil {
		return bail(errUnknownVolume)
	}

	if daemon.ProcessState != nil && daemon.ProcessState.Exited() {
		return bail(errZombie)
	}

	daemon.refs++

	daemon.Stdout = os.Stdout
	rc, err := daemon.StderrPipe()
	if err != nil {
		return bail(errBadRead{err})
	}

	if err := daemon.Start(); err != nil {
		return bail(err)
	}

	l, err := bufio.NewReader(io.TeeReader(rc, os.Stderr)).ReadString(byte('\n'))
	if err != nil {
		return bail(errBadRead{err})
	}

	if !strings.HasSuffix(l, "File system has been successfully mounted.\n") {
		return plugin.Response{Err: "Unexpected output from gcsfuse: \"" + l + "\""}
	}

	go io.Copy(os.Stderr, rc)

	return plugin.Response{Mountpoint: d.mountpoint(r.Name)}
}

func (d driver) Unmount(r plugin.Request) plugin.Response {
	d.Lock()
	defer d.Unlock()

	daemon := d.daemons[r.Name]

	if daemon == nil {
		return bail(errUnknownVolume)
	}

	daemon.Process.Signal(os.Interrupt)
	ps, err := daemon.Process.Wait()
	if err != nil {
		return bail(err)
	}
	if !ps.Success() {
		return bail(errDaemonDirty)
	}

	daemon.refs--

	if daemon.refs == 0 {
		delete(d.daemons, r.Name)
	}

	return plugin.Response{}
}

func (d driver) Remove(r plugin.Request) plugin.Response {
	return plugin.Response{}
}

func (d driver) Path(r plugin.Request) plugin.Response {
	return plugin.Response{Mountpoint: d.mountpoint(r.Name)}
}

func (d driver) mountpoint(name string) string {
	return filepath.Join(d.root, name)
}

// bail is just a shorthand for wrapping an error inside a plugin.Reponse
func bail(err error) plugin.Response {
	return plugin.Response{Err: err.Error()}
}
