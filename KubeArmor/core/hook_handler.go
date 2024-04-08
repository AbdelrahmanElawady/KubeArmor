// SPDX-License-Identifier: Apache-2.0
// Copyright 2022 Authors of KubeArmor

package core

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"

	kl "github.com/kubearmor/KubeArmor/KubeArmor/common"
	cfg "github.com/kubearmor/KubeArmor/KubeArmor/config"
	"github.com/kubearmor/KubeArmor/KubeArmor/types"
)

const kubearmorDir = "/var/run/kubearmor"

func (dm *KubeArmorDaemon) ListenToHook() {
	if err := os.MkdirAll(kubearmorDir, 0750); err != nil {
		log.Fatal(err)
	}

	listenPath := filepath.Join(kubearmorDir, "ka.sock")
	_ = os.Remove(listenPath) // in case kubearmor crashed and the socket wasn't removed
	socket, err := net.Listen("unix", listenPath)
	if err != nil {
		log.Fatal(err)
	}

	defer socket.Close()
	defer os.Remove(listenPath)

	for {
		conn, err := socket.Accept()
		if err != nil {
			log.Fatal(err)
		}

		go dm.handleConn(conn)
	}

}

func (dm *KubeArmorDaemon) handleConn(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 4096)

	for {
		n, err := conn.Read(buf)
		if err == io.EOF {
			return
		}
		if err != nil {
			log.Fatal(err)
		}

		data := struct {
			Operation string          `json:"operation"`
			Container types.Container `json:"container"`
		}{}

		err = json.Unmarshal(buf[:n], &data)
		if err != nil {
			log.Fatal(err)
		}
		dm.Logger.Printf("got container %s", data.Container.ContainerID)
		if data.Operation == "create" {
			dm.handleContainerCreate(data.Container)
		} else {
			dm.handleContainerStop(data.Container.ContainerID)
		}
	}
}
func (dm *KubeArmorDaemon) handleContainerCreate(container types.Container) {

	dm.ContainersLock.Lock()
	if len(dm.OwnerInfo) > 0 {
		container.Owner = dm.OwnerInfo[container.EndPointName]
	}
	dm.Containers[container.ContainerID] = container
	dm.Logger.Printf("added %s", container.ContainerID)
	dm.ContainersLock.Unlock()

	if dm.SystemMonitor != nil && cfg.GlobalCfg.Policy {
		dm.SystemMonitor.AddContainerIDToNsMap(container.ContainerID, container.NamespaceName, container.PidNS, container.MntNS)
		dm.RuntimeEnforcer.RegisterContainer(container.ContainerID, container.PidNS, container.MntNS)
	}
}
func (dm *KubeArmorDaemon) handleContainerStop(containerID string) {
	dm.ContainersLock.Lock()
	container, ok := dm.Containers[containerID]
	dm.Logger.Printf("deleted %s", containerID)
	if !ok {
		dm.ContainersLock.Unlock()
		return
	}
	delete(dm.Containers, containerID)
	dm.ContainersLock.Unlock()

	dm.EndPointsLock.Lock()
	for idx, endPoint := range dm.EndPoints {
		if endPoint.NamespaceName == container.NamespaceName && endPoint.EndPointName == container.EndPointName && kl.ContainsElement(endPoint.Containers, container.ContainerID) {

			// update apparmor profiles
			for idxA, profile := range endPoint.AppArmorProfiles {
				if profile == container.AppArmorProfile {
					dm.EndPoints[idx].AppArmorProfiles = append(dm.EndPoints[idx].AppArmorProfiles[:idxA], dm.EndPoints[idx].AppArmorProfiles[idxA+1:]...)
					break
				}
			}

			break
		}
	}
	dm.EndPointsLock.Unlock()

	if dm.SystemMonitor != nil && cfg.GlobalCfg.Policy {
		// update NsMap
		dm.SystemMonitor.DeleteContainerIDFromNsMap(containerID, container.NamespaceName, container.PidNS, container.MntNS)
		dm.RuntimeEnforcer.UnregisterContainer(containerID)
	}

}
