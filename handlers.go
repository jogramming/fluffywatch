package main

import (
	"errors"
	"fmt"
	"github.com/jonas747/fnet"
	"github.com/jonas747/plex"
	"log"
	//"strconv"
	"time"
)

type ErrResp struct {
	Err string `json:"error"`
}

func sendErrResp(session fnet.Session, err error, evtId int32) {
	errSend := netEngine.CreateAndSend(session, evtId, ErrResp{err.Error()})
	if errSend != nil {
		log.Println("Error sending error response: ", err)
		return
	}
}

func checkError(session fnet.Session, err error, evtId int32) bool {
	if err != nil {
		log.Println("Error occured while handling a request: ", err)
		sendErrResp(session, err, evtId)
		return true
	}

	return false
}

func checkBanned(session fnet.Session, respond bool) bool {
	id, _ := session.Data.GetString("id")
	// Check if banned
	configLock.RLock()
	defer configLock.RUnlock()
	for _, b := range config.Bans {
		if b == id {
			// banned!
			if respond {
				sendNotification(session, "You're banned from the chat, if you got unfairly banned message /u/jonas747", true)
			}
			return true
		}
	}
	ownIp := session.Conn.IP()
	for _, ip := range config.IPBans {
		//log.Printf(ip, ownIp)
		if ip == ownIp {
			if respond {
				sendNotification(session, "You're banned from the chat, if you got unfairly banned message /u/jonas747", true)
			}
			return true
		}
	}
	return false
}

func checkMod(session fnet.Session, respond bool) bool {
	configLock.RLock()
	defer configLock.RUnlock()

	if config.Master == "*" {
		return true
	}

	id, _ := session.Data.GetString("id")

	if id == config.Master {
		return true
	}

	for _, m := range config.Mods {
		if m == id {
			return true
		}
	}

	if respond {
		sendNotification(session, "You're not a mod", true)
	}
	return false
}

func checkMaster(session fnet.Session, respond bool) bool {
	configLock.RLock()
	defer configLock.RUnlock()

	if config.Master == "*" {
		return true
	}

	id, _ := session.Data.GetString("id")
	if config.Master == id {
		return true
	}

	if respond {
		sendNotification(session, "You're not an admin", true)
	}
	return false
}

type SetNameData struct {
	Name string `json:"name"`
	Old  string `json:"old"`
}

func handlerUserSetName(session fnet.Session, user SetNameData) {
	if user.Name == "" {
		user.Name = ">:)"
	}
	if len(user.Name) > 30 {
		user.Name = user.Name[:29]
	}

	viewersMutex.Lock()
	_, found := viewers[user.Name]
	if found {
		sendErrResp(session, errors.New("Name already in use"), EvtError)
		viewersMutex.Unlock()
		return
	}

	// Change the registered name
	oldName, _ := session.Data.GetString("name")

	temp := viewers[oldName]
	delete(viewers, oldName)
	viewers[user.Name] = temp
	viewersMutex.Unlock()

	// And finally here
	session.Data.Set("name", user.Name)

	user.Old = oldName

	err := netEngine.CreateAndSend(session, EvtSetName, user)
	if err != nil {
		log.Println("Error sending message: ", err)
		return
	}

	broadcastNotification(fmt.Sprintf("%s Changed their name to %s", oldName, user.Name), false)

	broadcastStatus()
	log.Printf("'%s' changed their name to '%s'\n", oldName, user.Name)
}

type SearchQuery struct {
	Title string `json:"title"`
	Kind  string `json:"kind"` // One of tv, movie
}

type SearchReply struct {
	Items []plex.PlexDirectory `json:"items"`
	Kind  string               `json:"kind"`
}

// func handleSearch(session fnet.Session, sq SearchQuery) {
// 	log.Println("Handling searchtv")
// 	if !checkMaster(session, true) {
// 		return
// 	}

// 	title := sq.Title
// 	if title == "" {
// 		sendErrResp(session, errors.New("Title is empty"), EvtSearch)
// 		return
// 	}

// 	var items []plex.PlexDirectory

// 	if sq.Kind == "tv" {
// 		mediaContainer, err := pms.FetchContainer("/library/all?type=2&title=" + title)
// 		if checkError(session, err, EvtSearch) {
// 			return
// 		}
// 		items = mediaContainer.Directories
// 	} else {
// 		mediaContainer, err := pms.FetchContainer("/library/all?type=1&title=" + title)
// 		if checkError(session, err, EvtSearch) {
// 			return
// 		}
// 		items = mediaContainer.Videos
// 	}

// 	if items == nil || len(items) < 1 {
// 		sendErrResp(session, errors.New("No search results! :("), EvtSearch)
// 		return
// 	}
// 	reply := SearchReply{items, sq.Kind}

// 	err := netEngine.CreateAndSend(session, EvtSearch, reply)
// 	if checkError(session, err, EvtSearch) {
// 		return
// 	}
// }

// type PlaylistAddItemReq struct {
// 	PlexItem plex.PlexDirectory `json:"plexItem"`

// 	Kind string
// 	// For tv shows
// 	Episode     int  `json:"episode"`
// 	Season      int  `json:"season"`
// 	AddAllAfter bool `json:"addAllAfter"`
// }

// func handlePlaylistAdd(session fnet.Session, paReq PlaylistAddItemReq) {
// 	log.Println("Handling playlistadd")
// 	if !checkMaster(session, true) {
// 		return
// 	}

// 	name, _ := session.Data.GetString("name")
// 	broadcastNotification(fmt.Sprintf("%s Added something to the playlist", name), true)

// 	switch paReq.Kind {
// 	case "tv":
// 		// Get all episodes and find the right one!
// 		uri := "/library/metadata/" + paReq.PlexItem.RatingKey + "/allLeaves"
// 		allEpisodes, err := pms.FetchContainer(uri)
// 		if err != nil {
// 			log.Println("Error adding playlist item, unable to access all episodes: ", err)
// 			return
// 		}

// 		// Find our episode
// 		for _, ep := range allEpisodes.Videos {
// 			index, _ := strconv.Atoi(ep.Index)
// 			parentIndex, _ := strconv.Atoi(ep.ParentIndex)
// 			if index == paReq.Episode && parentIndex == paReq.Season {
// 				// found it
// 				err := player.AddPlaylistItemByPlexVideo(ep, ITEMTYPETV)
// 				if err != nil {
// 					log.Println("Error adding playlist item: ", err)
// 				}
// 			} else if paReq.AddAllAfter && (parentIndex > paReq.Season || (parentIndex == paReq.Season && index > paReq.Episode)) {
// 				// If were adding all after selected
// 				err := player.AddPlaylistItemByPlexVideo(ep, ITEMTYPETV)
// 				if err != nil {
// 					log.Println("Error adding playlist item: ", err)
// 				}
// 			}
// 		}

// 	case "movie":
// 		fullVideoContainer, err := pms.FetchContainer(paReq.PlexItem.Key)
// 		if checkError(session, err, EvtPlaylistAdd) {
// 			return
// 		}
// 		err = player.AddPlaylistItemByPlexVideo(fullVideoContainer.Videos[0], ITEMTYPEMOVIE)
// 		if checkError(session, err, EvtPlaylistAdd) {
// 			return
// 		}
// 	}

// 	// Broadcast the new playlist
// 	wm, err := buildPlaylistMessage()
// 	if checkError(session, err, EvtPlaylistAdd) {
// 		return
// 	}
// 	netEngine.Broadcast(wm)
// }

type StatusReply struct {
	Timestamp int             `json:"timestamp"`
	Action    string          `json:"action"`
	Viewers   map[string]bool `json:"viewers"`
	Playing   bool            `json:"playing"`
}

// Responds with the status
func handleStatus(session fnet.Session) {
	log.Println("Handling status")

	wm, err := buildStatusMessage()
	if checkError(session, err, EvtStatus) {
		return
	}
	session.Conn.Send(wm)
}

// Responds with the current playlist
func handlePlaylist(session fnet.Session) {
	log.Println("Handling playlist")

	wm, err := buildPlaylistMessage()
	if checkError(session, err, EvtPlaylist) {
		return
	}
	session.Conn.Send(wm)
}

// Responds with the settings
func handleSettings(session fnet.Session) {
	log.Println("Handling settings")

	wm, err := buildSettingsMessage()
	if checkError(session, err, EvtSettings) {
		return
	}
	session.Conn.Send(wm)
}

type PlayRequest struct {
	Index int `json:"index"`
}

func handlePlay(session fnet.Session, pr PlayRequest) {
	if !checkMaster(session, true) {
		return
	}

	player.Lock.Lock()
	defer player.Lock.Unlock()

	if pr.Index != -1 {
		// Play a specified playlist element instead

		if player.Playing {
			player.CurrentPlaylist.CurrentIndex = pr.Index - 1
		} else {
			player.CurrentPlaylist.CurrentIndex = pr.Index
		}

		// finally stop the stream to trigger the next(slected) playlist elent
		if player.Playing {
			player.CmdChan <- PCMDNEXT
		} else {
			go player.Play()
			name, _ := session.Data.GetString("name")
			broadcastNotification(fmt.Sprintf("%s Pressed play", name), true)
		}
	} else {
		if player.Playing {
			sendErrResp(session, errors.New("Already playing"), EvtPlay)
			return
		}

		go player.Play()
		name, _ := session.Data.GetString("name")
		broadcastNotification(fmt.Sprintf("%s Pressed play", name), true)
	}
}

func handlePause(session fnet.Session) {
	if !checkMaster(session, true) {
		return
	}

	player.Lock.Lock()
	defer player.Lock.Unlock()

	if !player.Playing {
		sendErrResp(session, errors.New("Not playing anything at the moment"), EvtPause)
		return
	}

	player.CmdChan <- PCMDSTOP
	name, _ := session.Data.GetString("name")
	broadcastNotification(fmt.Sprintf("%s Pressed pause", name), true)
}

func handleNext(session fnet.Session) {
	if !checkMaster(session, true) {
		return
	}

	player.CmdChan <- PCMDNEXT
	name, _ := session.Data.GetString("name")
	broadcastNotification(fmt.Sprintf("%s Pressed next", name), true)
}
func handlePrevious(session fnet.Session) {
	if !checkMaster(session, true) {
		return
	}

	player.CmdChan <- PCMDPREV
	name, _ := session.Data.GetString("name")
	broadcastNotification(fmt.Sprintf("%s Pressed previous", name), true)
}

func handlePlaylistClear(session fnet.Session) {
	if !checkMaster(session, true) {
		return
	}

	player.Lock.Lock()
	defer player.Lock.Unlock()

	player.CurrentPlaylist.Items = make([]PlaylistItem, 0)
	name, _ := session.Data.GetString("name")
	broadcastNotification(fmt.Sprintf("%s Cleared the playlist", name), true)
	go broadcastPlaylistStatus()
}

func handleSetSettings(session fnet.Session, settings TranscoderSettings) {
	if !checkMaster(session, true) {
		return
	}

	// Check if the settings are valig
	err := ValidatePreset(settings.Preset)
	if err != nil {
		sendErrResp(session, err, EvtSetSettings)
		return
	}

	player.Lock.Lock()
	player.Settings = settings
	player.Lock.Unlock()
	name, _ := session.Data.GetString("name")
	broadcastNotification(fmt.Sprintf("%s Changed the transcoder settings", name), true)
	if settings.Subs {
		log.Println("Subs are enabled!")
	} else {
		log.Println("Subs are disabled!")
	}

}

type WatchingStatusUpdate struct {
	Watching bool `json:"watching"`
}

func handleWatchingStatusUpdate(session fnet.Session, wsu WatchingStatusUpdate) {
	name, _ := session.Data.GetString("name")
	//vChangeChan <- ViewerChange{Name: name, Watching: wsu.Watching}
	session.Data.Set("watching", wsu.Watching)

	isWatching := "watching"
	if !wsu.Watching {
		isWatching = "not watching"
	}

	broadcastNotification(fmt.Sprintf("%s changed state to: %s", name, isWatching), false)
}

type ChatMessage struct {
	Msg  string `json:"msg"`
	From string `json:"from"`
	Kind string `json:"kind"`
}

func handleChatMessage(session fnet.Session, cm ChatMessage) {
	id, exists := session.Data.GetString("id")
	if !exists {
		sendNotification(session, "You do not appear to have an id?..", true)
		return
	}

	// Check if banned
	if checkBanned(session, true) {
		return
	}

	if len(cm.Msg) > 1000 {
		sendNotification(session, "Too long chat message, cant be longer than 1k characters", true)
		return
	}

	last, exists := session.Data.Get("lastchat")
	if exists {
		cast := last.(time.Time)

		since := time.Since(cast)
		if since.Seconds() < 0.5 {
			sendNotification(session, "You can send a maximum of 1 chat message per 0.5 second", true)
			return
		}
	}
	session.Data.Set("lastchat", time.Now())

	from, _ := session.Data.GetString("name")

	bcm := ChatMessage{Msg: cm.Msg, From: from}

	if checkMaster(session, false) {
		bcm.Kind = "master"
	} else if checkMod(session, false) {
		bcm.Kind = "mod"
	} else {
		bcm.Kind = "user"
	}

	log.Printf("Chat msg {%s}[%s][%s]'%s':%s\n", session.Conn.IP(), id, bcm.Kind, bcm.From, bcm.Msg)
	err := netEngine.CreateAndBroadcast(EvtChatMessage, bcm)
	if err != nil {
		log.Println("Error creating and sending chat message", err)
		return
	}
}

func handleAuth(session fnet.Session, key string) {
	log.Println("Attempting to authenticate with key ", key)

	last, exists := session.Data.Get("lastauth")
	if exists {
		cast := last.(time.Time)

		since := time.Since(cast)
		if since.Seconds() < 5 {
			sendNotification(session, "Maximum 1 login try every 5 second", true)
			return
		}
	}
	session.Data.Set("lastauth", time.Now())
	session.Data.Set("id", key)
}

type chatCmd struct {
	Cmd    string `json:"cmd"`
	Target string `json:"target"`
}

func handleChatCmd(session fnet.Session, data chatCmd) {
	log.Println("Handling chatcmd", data.Cmd)

	viewersMutex.RLock()
	targetSession, exists := viewers[data.Target]
	viewersMutex.RUnlock()

	if !exists {
		sendNotification(session, "couldn't find user '"+data.Target+"'", true)
		return
	}

	targetId, found := targetSession.Data.GetString("id")
	if !found {
		sendNotification(session, "User has no id '"+data.Target+"'", true)
		return
	}

	ownName, _ := session.Data.GetString("name")
	ownId, _ := session.Data.GetString("id")

	switch data.Cmd {
	case "/mod":
		log.Println("Adding mod", data.Target)
		if !checkMaster(session, true) {
			return
		}
		err := addMod(targetId)
		if err != nil {
			sendNotification(session, "Error: "+err.Error(), true)
		}
		sendNotification(session, "Added mod "+data.Target, true)
	case "/demod":
		if !checkMaster(session, true) {
			return
		}
		err := removeMod(targetId)
		if err != nil {
			sendNotification(session, "Error: "+err.Error(), true)
		}
		sendNotification(session, "Removed mod "+data.Target, true)
	case "/ban":
		if !checkMod(session, true) {
			return
		}
		if checkMod(targetSession, false) {
			sendNotification(session, "Cannot ban other mods", true)
			return
		}

		err := banUser(targetId)
		if err != nil {
			sendNotification(session, "Error: "+err.Error(), true)
		}
		sendNotification(session, "Banned user "+data.Target, true)
		log.Printf("{%s}[%s] '%s' Banned '%s' [%s]\n", targetSession.Conn.IP(), ownId, ownName, data.Target, targetId)
	case "/unban":
		if !checkMod(session, true) {
			return
		}

		err := unBanUser(targetId)
		if err != nil {
			sendNotification(session, "Error: "+err.Error(), true)
		}
		sendNotification(session, "Unbanned user "+data.Target, true)
		log.Printf("{%s}[%s] '%s' UnBanned '%s' [%s]\n", targetSession.Conn.IP(), ownId, ownName, data.Target, targetId)

	case "/ipban":
		if !checkMod(session, true) {
			return
		}
		err := banIP(targetSession.Conn.IP())
		if err != nil {
			sendNotification(session, "Error: "+err.Error(), true)
		}
		sendNotification(session, "banned ip "+data.Target, true)
		log.Printf("{%s}[%s] '%s' ipbanned '%s' [%s]\n", targetSession.Conn.IP(), ownId, ownName, data.Target, targetSession.Conn.IP())

	case "/ipunban":
		if !checkMod(session, true) {
			return
		}

		err := unBanIP(targetSession.Conn.IP())
		if err != nil {
			sendNotification(session, "Error: "+err.Error(), true)
		}
		sendNotification(session, "unbanned ip "+data.Target, true)
		log.Printf("{%s}[%s] '%s' ip unbanned '%s' [%s]\n", targetSession.Conn.IP(), ownId, ownName, data.Target, targetSession.Conn.IP())
	}
}

func handleReloadPlaylist(session fnet.Session) {
	if !checkMaster(session, true) {
		return
	}
	loadPlaylist(flagPlaylistPath)
	broadcastPlaylistStatus()
}
