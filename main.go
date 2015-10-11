package main

/*

 */

import (
	"errors"
	"flag"
	"fmt"
	"github.com/jonas747/fnet"
	"github.com/jonas747/fnet/ws"
	"github.com/jonas747/plex"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	EvtSetName             int32 = 1
	EvtPlaylist                  = 2
	EvtStatus                    = 3
	EvtSearch                    = 4
	EvtPlaylistAdd               = 5
	EvtPlaylistRemove            = 6
	EvtPlaylistMove              = 7
	EvtSettings                  = 8
	EvtSetSettings               = 9
	EvtPlay                      = 10
	EvtPause                     = 11
	EvtNext                      = 12
	EvtPrev                      = 13
	EvtSeek                      = 14
	EvtPlaylistClear             = 15
	EvtUserJoin                  = 16
	EvtUserLeave                 = 17
	EvtWatchingStateChange       = 18
	EvtChatMessage               = 19
	EvtNotification              = 20
	EvtError                     = 21
)

var (
	// Valid presets for x264
	ValidPresets = []string{"ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow"}
	//viewers      *int32
)

// Flags
var (
	flagPW   = flag.String("pw", "", "Password needed to controll this server")
	flagAddr = flag.String("addr", ":7447", "Address  to listen to")
)

var (
	player    *Player
	pms       *plex.PlexServer
	netEngine *fnet.Engine

	vChangeChan  = make(chan ViewerChange)
	viewers      = make(map[string]bool)
	viewersMutex sync.RWMutex
	idGenChan    = make(chan int64)
)

func main() {
	flag.Parse()

	go incIdGen(idGenChan)

	rand.Seed(time.Now().UTC().UnixNano())

	pms = &plex.PlexServer{
		Path: "http://192.168.1.10:32400",
	}

	player = NewPlayer("rtmp://jonas747.com/cinema/live")
	go player.Monitor()

	netEngine = fnet.DefaultEngine()
	netEngine.Encoder = fnet.JsonEncoder{} // Use json instead of protocol buffers
	netEngine.OnConnOpen = onOpenConn
	netEngine.OnConnClose = onClosedConn

	AddHandlers(netEngine)

	listener := &ws.WebsocketListener{
		Engine: netEngine,
		Addr:   *flagAddr,
	}

	go netEngine.AddListener(listener)
	go netEngine.ListenChannels()
	go sessionWatcher()
	listenErrors(netEngine)
}

func AddHandlers(engine *fnet.Engine) {
	engine.AddHandler(fnet.NewHandlerSafe(handlerUserSetName, EvtSetName))
	engine.AddHandler(fnet.NewHandlerSafe(handleSearch, EvtSearch))
	engine.AddHandler(fnet.NewHandlerSafe(handleStatus, EvtStatus))
	engine.AddHandler(fnet.NewHandlerSafe(handlePlaylist, EvtPlaylist))
	engine.AddHandler(fnet.NewHandlerSafe(handleSettings, EvtSettings))
	engine.AddHandler(fnet.NewHandlerSafe(handleSetSettings, EvtSetSettings))
	engine.AddHandler(fnet.NewHandlerSafe(handlePlaylistAdd, EvtPlaylistAdd))
	engine.AddHandler(fnet.NewHandlerSafe(handlePlaylistClear, EvtPlaylistClear))
	engine.AddHandler(fnet.NewHandlerSafe(handlePlay, EvtPlay))
	engine.AddHandler(fnet.NewHandlerSafe(handlePause, EvtPause))
	engine.AddHandler(fnet.NewHandlerSafe(handleNext, EvtNext))
	engine.AddHandler(fnet.NewHandlerSafe(handlePrevious, EvtPrev))
	engine.AddHandler(fnet.NewHandlerSafe(handleWatchingStatusUpdate, EvtWatchingStateChange))
	engine.AddHandler(fnet.NewHandlerSafe(handleChatMessage, EvtChatMessage))
}

func LogSendError(r *http.Request, err error) {
	if err == nil {
		return
	}
	fmt.Printf("Error sending response to [%s] Error: %s", r.RemoteAddr, err.Error())
}

func ValidatePath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return errors.New("Error opening file: " + err.Error())
	}
	if info.IsDir() {
		// We cant stream a directory, silly you
		return errors.New("WHY THE FUCK ARE YOU TRYING TO STREAM A DIRECTORY YOU PIECE OF SHIT GO DIE")
	}
	return nil
}

func ValidatePreset(preset string) error {
	found := false
	for _, p := range ValidPresets {
		if p == preset {
			found = true
			break
		}
	}
	if !found {
		return errors.New("Invalid preset, check for typos and spaces at the beginning or end")
	}
	return nil
}

type ViewerChange struct {
	Name     string
	Watching bool
}

func sessionWatcher() {
	for {
		select {
		case change := <-vChangeChan:
			viewersMutex.Lock()
			viewers[change.Name] = change.Watching
			viewersMutex.Unlock()
			broadcastStatus()
		}
	}
}

func onClosedConn(session fnet.Session) {
	name, exists := session.Data.GetString("name")
	if exists {
		viewersMutex.Lock()
		delete(viewers, name)
		viewersMutex.Unlock()
		broadcastNotification(fmt.Sprintf("%s Left :'(", name))
	}

	fmt.Println(name, " disconnected!")
	broadcastStatus()
}

func onOpenConn(session fnet.Session) {
	fmt.Println("Someone connected!")
	pl, err := buildPlaylistMessage()
	if err != nil {
		fmt.Println("Error building playlist message!: ", err)
		return
	}
	session.Conn.Send(pl)

	id := <-idGenChan
	fname := fmt.Sprintf("dude#%d", id)
	session.Data.Set("name", fname)

	vChangeChan <- ViewerChange{Name: fname, Watching: false}

	broadcastNotification(fmt.Sprintf("%s Joined", fname))
}

func listenErrors(engine *fnet.Engine) {
	for {
		err := <-engine.ErrChan
		fmt.Printf("fnet Error: ", err.Error())
	}
}

func incIdGen(out chan int64) {
	curId := int64(0)
	for {
		out <- curId
		curId++
	}
}

type Notification struct {
	Msg string `json:"msg"`
}

func broadcastNotification(notification string) {
	n := Notification{notification}

	wm, err := netEngine.CreateWireMessage(EvtNotification, n)
	if err != nil {
		fmt.Println("Error creating notification message: ", err)
		return
	}
	netEngine.Broadcast(wm)
}
