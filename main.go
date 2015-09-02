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
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	plugin "github.com/calavera/dkvolume"
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

// driver wraps multiple gcsfuse processes
type driver struct {
	*sync.Mutex

	// Maps bucket to the gcfsfuse command that owns the bucket.
	cmds map[string]*exec.Cmd
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
		cmds:  make(map[string]*exec.Cmd),
	}

	h := plugin.NewHandler(d)
	log.Printf("Listening on %s with mount target %s\n", socketAddress, root)
	log.Println(h.ServeUnix("root", socketAddress))
}

func (d driver) Mount(r plugin.Request) plugin.Response {
	d.Lock()
	defer d.Unlock()

	b := d.bucket(r.Name)

	daemon := d.cmds[b]

	if daemon != nil {
		if daemon.ProcessState != nil && daemon.ProcessState.Exited() {
			return bail(errZombie)
		}
		return plugin.Response{}
	}

	mnt := d.mountpoint(b)

	if err := os.MkdirAll(mnt, os.ModeTemporary); err != nil {
		return bail(err)
	}

	daemon = exec.Command("gcsfuse", append(os.Args[1:len(os.Args)-1], b, mnt)...)
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

	d.cmds[b] = daemon

	go io.Copy(os.Stderr, rc)

	return plugin.Response{Mountpoint: d.mountpoint(r.Name)}
}

func (d driver) Remove(r plugin.Request) plugin.Response {
	d.Lock()
	defer d.Unlock()

	b := d.bucket(r.Name)

	daemon := d.cmds[b]

	if daemon == nil {
		log.Printf("Doing nothing when asked to remove volume for %s ...", r.Name)
		return plugin.Response{}
	}

	log.Printf("Interrupting gcsfuse %s", b)
	daemon.Process.Signal(os.Interrupt)
	ps, err := daemon.Process.Wait()
	if err != nil {
		log.Printf("Waiting for gcsfuse %s errored, returning error.", b)
		return bail(err)
	}
	if !ps.Success() {
		log.Printf("gcsfuse %s exited dirty, returning error.", b)
		return bail(errDaemonDirty)
	}

	return plugin.Response{}
}

func (d driver) Create(r plugin.Request) plugin.Response {
	return plugin.Response{}
}

func (d driver) Unmount(r plugin.Request) plugin.Response {
	return plugin.Response{}
}

func (d driver) Path(r plugin.Request) plugin.Response {
	return plugin.Response{Mountpoint: d.mountpoint(r.Name)}
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

// bail is just a shorthand for wrapping an error inside a plugin.Reponse
func bail(err error) plugin.Response {
	return plugin.Response{Err: err.Error()}
}
