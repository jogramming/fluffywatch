package main

import (
	"errors"
	"fmt"
	"github.com/jonas747/fnet"
	"github.com/jonas747/plex"
	"strconv"
	"time"
)

type ErrResp struct {
	Err string `json:"error"`
}

func sendErrResp(session fnet.Session, err error, evtId int32) {
	errSend := netEngine.CreateAndSend(session, evtId, ErrResp{err.Error()})
	if errSend != nil {
		fmt.Println("Error sending error response: ", err)
		return
	}
}

func checkError(session fnet.Session, err error, evtId int32) bool {
	if err != nil {
		fmt.Println("Error occured while handling a request: ", err)
		sendErrResp(session, err, evtId)
		return true
	}

	return false
}

func checkAuth(session fnet.Session) bool {
	if *flagPW == "" {
		return true
	}

	isAuth, exists := session.Data.GetBool("auth")
	if exists && isAuth {
		return true
	}

	sendNotification(session, "You have to authenticate to do that")
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

	viewersMutex.RLock()
	_, found := viewers[user.Name]
	if found {
		sendErrResp(session, errors.New("Name already in use"), EvtError)
		viewersMutex.RUnlock()
		return
	}
	viewersMutex.RUnlock()

	// Change the registered name
	oldName, _ := session.Data.GetString("name")

	viewersMutex.Lock()
	watching := viewers[oldName]
	delete(viewers, oldName)
	viewers[user.Name] = watching
	viewersMutex.Unlock()

	// And finally here
	session.Data.Set("name", user.Name)

	user.Old = oldName

	err := netEngine.CreateAndSend(session, EvtSetName, user)
	if err != nil {
		fmt.Println("Error sending message: ", err)
		return
	}

	broadcastNotification(fmt.Sprintf("%s Changed their name to %s", oldName, user.Name))

	broadcastStatus()
	fmt.Println("Someone set their name to:", user.Name)
}

type SearchQuery struct {
	Title string `json:"title"`
	Kind  string `json:"kind"` // One of tv, movie
}

type SearchReply struct {
	Items []plex.PlexDirectory `json:"items"`
	Kind  string               `json:"kind"`
}

func handleSearch(session fnet.Session, sq SearchQuery) {
	fmt.Println("Handling searchtv")
	if !checkAuth(session) {
		return
	}

	title := sq.Title
	if title == "" {
		sendErrResp(session, errors.New("Title is empty"), EvtSearch)
		return
	}

	var items []plex.PlexDirectory

	if sq.Kind == "tv" {
		mediaContainer, err := pms.FetchContainer("/library/all?type=2&title=" + title)
		if checkError(session, err, EvtSearch) {
			return
		}
		items = mediaContainer.Directories
	} else {
		mediaContainer, err := pms.FetchContainer("/library/all?type=1&title=" + title)
		if checkError(session, err, EvtSearch) {
			return
		}
		items = mediaContainer.Videos
	}

	if items == nil || len(items) < 1 {
		sendErrResp(session, errors.New("No search results! :("), EvtSearch)
		return
	}
	reply := SearchReply{items, sq.Kind}

	err := netEngine.CreateAndSend(session, EvtSearch, reply)
	if checkError(session, err, EvtSearch) {
		return
	}
}

type PlaylistAddItemReq struct {
	PlexItem plex.PlexDirectory `json:"plexItem"`

	Kind string
	// For tv shows
	Episode     int  `json:"episode"`
	Season      int  `json:"season"`
	AddAllAfter bool `json:"addAllAfter"`
}

func handlePlaylistAdd(session fnet.Session, paReq PlaylistAddItemReq) {
	fmt.Println("Handling playlistadd")
	if !checkAuth(session) {
		return
	}

	name, _ := session.Data.GetString("name")
	broadcastNotification(fmt.Sprintf("%s Added something to the playlist", name))

	switch paReq.Kind {
	case "tv":
		// Get all episodes and find the right one!
		uri := "/library/metadata/" + paReq.PlexItem.RatingKey + "/allLeaves"
		allEpisodes, err := pms.FetchContainer(uri)
		if err != nil {
			fmt.Println("Error adding playlist item, unable to access all episodes: ", err)
			return
		}

		// Find our episode
		for _, ep := range allEpisodes.Videos {
			index, _ := strconv.Atoi(ep.Index)
			parentIndex, _ := strconv.Atoi(ep.ParentIndex)
			if index == paReq.Episode && parentIndex == paReq.Season {
				// found it
				err := player.AddPlaylistItemByPlexVideo(ep, ITEMTYPETV)
				if err != nil {
					fmt.Println("Error adding playlist item: ", err)
				}
			} else if paReq.AddAllAfter && (parentIndex > paReq.Season || (parentIndex == paReq.Season && index > paReq.Episode)) {
				// If were adding all after selected
				err := player.AddPlaylistItemByPlexVideo(ep, ITEMTYPETV)
				if err != nil {
					fmt.Println("Error adding playlist item: ", err)
				}
			}
		}

	case "movie":
		fullVideoContainer, err := pms.FetchContainer(paReq.PlexItem.Key)
		if checkError(session, err, EvtPlaylistAdd) {
			return
		}
		err = player.AddPlaylistItemByPlexVideo(fullVideoContainer.Videos[0], ITEMTYPEMOVIE)
		if checkError(session, err, EvtPlaylistAdd) {
			return
		}
	}

	// Broadcast the new playlist
	wm, err := buildPlaylistMessage()
	if checkError(session, err, EvtPlaylistAdd) {
		return
	}
	netEngine.Broadcast(wm)
}

type StatusReply struct {
	Timestamp int             `json:"timestamp"`
	Action    string          `json:"action"`
	Viewers   map[string]bool `json:"viewers"`
	Playing   bool            `json:"playing"`
}

// Responds with the status
func handleStatus(session fnet.Session) {
	fmt.Println("Handling status")

	wm, err := buildStatusMessage()
	if checkError(session, err, EvtStatus) {
		return
	}
	session.Conn.Send(wm)
}

// Responds with the current playlist
func handlePlaylist(session fnet.Session) {
	fmt.Println("Handling playlist")

	wm, err := buildPlaylistMessage()
	if checkError(session, err, EvtPlaylist) {
		return
	}
	session.Conn.Send(wm)
}

// Responds with the settings
func handleSettings(session fnet.Session) {
	fmt.Println("Handling settings")

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
	if !checkAuth(session) {
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
			broadcastNotification(fmt.Sprintf("%s Pressed play", name))
		}
	} else {
		if player.Playing {
			sendErrResp(session, errors.New("Already playing"), EvtPlay)
			return
		}

		go player.Play()
		name, _ := session.Data.GetString("name")
		broadcastNotification(fmt.Sprintf("%s Pressed play", name))
	}
}

func handlePause(session fnet.Session) {
	if !checkAuth(session) {
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
	broadcastNotification(fmt.Sprintf("%s Pressed pause", name))
}

func handleNext(session fnet.Session) {
	if !checkAuth(session) {
		return
	}

	player.CmdChan <- PCMDNEXT
	name, _ := session.Data.GetString("name")
	broadcastNotification(fmt.Sprintf("%s Pressed next", name))
}
func handlePrevious(session fnet.Session) {
	if !checkAuth(session) {
		return
	}

	player.CmdChan <- PCMDPREV
	name, _ := session.Data.GetString("name")
	broadcastNotification(fmt.Sprintf("%s Pressed previous", name))
}

func handlePlaylistClear(session fnet.Session) {
	if !checkAuth(session) {
		return
	}

	player.Lock.Lock()
	defer player.Lock.Unlock()

	player.CurrentPlaylist.Items = make([]PlaylistItem, 0)
	name, _ := session.Data.GetString("name")
	broadcastNotification(fmt.Sprintf("%s Cleared the playlist", name))

}

func handleSetSettings(session fnet.Session, settings TranscoderSettings) {
	if !checkAuth(session) {
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
	broadcastNotification(fmt.Sprintf("%s Changed the transcoder settings", name))
	if settings.Subs {
		fmt.Println("Subs are enabled!")
	} else {
		fmt.Println("Subs are disabled!")
	}

}

type WatchingStatusUpdate struct {
	Watching bool `json:"watching"`
}

func handleWatchingStatusUpdate(session fnet.Session, wsu WatchingStatusUpdate) {
	name, _ := session.Data.GetString("name")
	vChangeChan <- ViewerChange{Name: name, Watching: wsu.Watching}

	isWatching := "watching"
	if !wsu.Watching {
		isWatching = "not watching"
	}

	broadcastNotification(fmt.Sprintf("%s changed state to: %s", name, isWatching))
}

type ChatMessage struct {
	Msg  string `json:"msg"`
	From string `json:"from"`
}

func handleChatMessage(session fnet.Session, cm ChatMessage) {
	last, exists := session.Data.Get("lastchat")

	from, _ := session.Data.GetString("name")
	if exists {
		cast := last.(time.Time)

		since := time.Since(cast)
		if since.Seconds() < 1 {
			sendNotification(session, "You can send a maximum of 1 chat message per second")
			return
		}
	}
	session.Data.Set("lastchat", time.Now())

	bcm := ChatMessage{Msg: cm.Msg, From: from}
	err := netEngine.CreateAndBroadcast(EvtChatMessage, bcm)
	if err != nil {
		fmt.Println("Error creating and sending chat message", err)
		return
	}
}

func handleAuth(session fnet.Session, key string) {
	fmt.Println("Attempting to authenticate with key ", key)

	returnMessage := "Failed logging in, invalid key?"
	if *flagPW == key {
		session.Data.Set("auth", true)
		returnMessage = "Successfully authenticated!"
	}

	if *flagPW == "" {
		returnMessage = "There is no password set on the server"
	}

	// bcm := ChatMessage{Msg: returnMessage, From: "sys"}
	// err := netEngine.CreateAndSend(session, EvtChatMessage, bcm)
	// if err != nil {
	// 	fmt.Println("Error creating chat wire message", err)
	// }
	sendNotification(session, returnMessage)
}
