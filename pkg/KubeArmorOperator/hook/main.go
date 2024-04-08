// SPDX-License-Identifier: Apache-2.0
// Copyright 2022 Authors of KubeArmor

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/kubearmor/KubeArmor/KubeArmor/types"
	"github.com/opencontainers/runtime-spec/specs-go"
)

var (
	kubeArmorSocket string
)

func main() {
	flag.StringVar(&kubeArmorSocket, "kubearmor-socket", "/var/run/kubearmor/ka.sock", "KubeArmor socket")
	flag.Parse()
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	state := specs.State{}
	err = json.Unmarshal(input, &state)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := run(state); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

}

func run(state specs.State) error {
	container := types.Container{
		ContainerID:   state.ID,
		ContainerName: state.Annotations["io.kubernetes.container.name"],
		NamespaceName: state.Annotations["io.kubernetes.pod.namespace"],
		EndPointName:  state.Annotations["io.kubernetes.pod.name"],
	}
	container.AppArmorProfile = state.Annotations[fmt.Sprintf("container.apparmor.security.beta.kubernetes.io/%s", container.ContainerName)]
	container.AppArmorProfile = strings.TrimPrefix(container.AppArmorProfile, "localhost/")

	if state.Status != specs.StateStopped {
		nsPath := fmt.Sprintf("/proc/%d/ns", state.Pid)

		pidLink, err := os.Readlink(filepath.Join(nsPath, "pid"))
		if err != nil {
			return err
		}
		if _, err := fmt.Sscanf(pidLink, "pid:[%d]\n", &container.PidNS); err != nil {
			return err
		}
		mntLink, err := os.Readlink(filepath.Join(nsPath, "mnt"))
		if err != nil {
			return err
		}
		if _, err := fmt.Sscanf(mntLink, "mnt:[%d]\n", &container.MntNS); err != nil {
			return err
		}
	}
	data := struct {
		Operation string          `json:"operation"`
		Container types.Container `json:"container"`
	}{Operation: "create", Container: container}

	if state.Status == specs.StateStopped {
		data.Operation = "stop"

	}
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return err
	}

	conn, err := net.Dial("unix", kubeArmorSocket)
	if err != nil {
		// not returning error here because this can happen in multiple cases
		// that we don't want container creation to be blocked on:
		// - hook was created before KubeArmor was running so the socket doesn't exist yet
		// - KubeArmor crashed so there is nothing listening on socket
		return nil
	}
	defer conn.Close()

	_, err = conn.Write(dataJSON)
	if err != nil {
		return err
	}
	return nil
}
