package main

import (
	"fmt"
	"time"
)

func buildStatusMessage() ([]byte, error) {
	player.Lock.Lock()
	defer player.Lock.Unlock()

	timestamp := 0
	if player.Playing {
		d := time.Now().Sub(player.StartedPlaying)
		timestamp = int(d.Seconds())
	} else {
		d := player.StoppedPlaying.Sub(player.StartedPlaying)
		timestamp = int(d.Seconds())
	}

	action := "Playing"
	if !player.Playing {
		if player.ManualStop {
			action = "Paused"
		} else {
			action = "Finished"
		}
	}

	viewersMutex.RLock()
	stReply := StatusReply{
		Timestamp: timestamp,
		Action:    action,
		Viewers:   viewers,
		Playing:   player.Playing,
	}
	viewersMutex.RUnlock()
	wm, err := netEngine.CreateWireMessage(EvtStatus, stReply)
	return wm, err
}

func buildPlaylistMessage() ([]byte, error) {
	player.Lock.Lock()
	defer player.Lock.Unlock()

	pl := player.CurrentPlaylist
	wm, err := netEngine.CreateWireMessage(EvtPlaylist, pl)
	return wm, err
}

func buildSettingsMessage() ([]byte, error) {
	player.Lock.Lock()
	defer player.Lock.Unlock()

	settings := player.Settings
	wm, err := netEngine.CreateWireMessage(EvtSettings, settings)
	return wm, err
}

func broadcastPlaylistStatus() {
	wm1, err := buildPlaylistMessage()
	if err != nil {
		fmt.Println("Error broadcasting playliststatus: ", err)
		return
	}
	netEngine.Broadcast(wm1)

	broadcastStatus()
}

func broadcastStatus() {
	wm, err := buildStatusMessage()
	if err != nil {
		fmt.Println("Error broadcasting status: ", err)
		return
	}
	netEngine.Broadcast(wm)
}
