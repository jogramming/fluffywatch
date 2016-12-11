package main

/*

 */

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/jonas747/fnet"
	"github.com/jonas747/fnet/ws"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
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
	EvtAuth                      = 22
	EvtChatCmd                   = 23
	EvtReloadPlaylist            = 24
)

const VERSION = "3.0.0 (2016/12/08)"

var (
	// Valid presets for x264
	ValidPresets = []string{"ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow"}
	//viewers      *int32
)

// Flags
var (
	// flagPW        = flag.String("pw", "", "Password needed to controll this server")
	// flagAddr      = flag.String("addr", ":7447", "Address  to listen to")
	// startPlaylist = flag.String("playlist", "", "A text file containing a list of video paths seperated by newline")
	// publishPath   = flag.String("push", "rtmp://jonas747.com/cinema/live", "Where to publish to")
	flagPlaylistPath string
	configPath       string
)

func init() {
	flag.StringVar(&configPath, "config", "config.json", "Path to config")
	flag.StringVar(&flagPlaylistPath, "playlist", "playlist", "Path to playlist")
}

type Config struct {
	Master          string   `json:"master"`
	Mods            []string `json:"mods"`
	Listen          string   `json:"listen"`
	PlaylistPath    string   `json:"playlistPath"`
	HLSPlaylistPath string   `json:"hls_playlist_path"`
	SegmentDir      string   `json:"segment_dir"`
	Playlist        []string `json:"-"`
	Bans            []string `json:"bans"`
	IPBans          []string `json:"ipBans"`
}

var (
	configLock     sync.RWMutex
	config         *Config
	lastConfigLoad time.Time
)

var (
	player *Player
	// pms       *plex.PlexServer
	netEngine *fnet.Engine

	viewers      = make(map[string]fnet.Session)
	viewersMutex sync.RWMutex
	idGenChan    = make(chan int64)
)

func main() {
	flag.Parse()

	logger := newLogger()
	go logger.Writer()
	log.SetOutput(logger)

	log.Printf("\n\n########\nSTARTING %s\n#######\n\n", VERSION)

	go incIdGen(idGenChan)

	rand.Seed(time.Now().UTC().UnixNano())

	// roundTripper := http.DefaultTransport
	// transport := roundTripper.(*http.Transport)
	// transport.TLSClientConfig = &tls.Config{
	// 	InsecureSkipVerify: true,
	// }

	// httpClient := &http.Client{
	// 	Transport: transport,
	// }

	// pms = &plex.PlexServer{
	// 	Path:   "https://192.168.1.10:32400",
	// 	Client: httpClient,
	// }

	c, err := loadConfig(configPath)
	if err != nil {
		config = &Config{
			Master: "*",
			Mods:   make([]string, 0),
			Listen: ":7449",
			//Publish: "rtmp://jonas747.com/cinema/live",
		}
	} else {
		lastConfigLoad = time.Now()
		config = c
		go configLoader(configPath)
	}

	player = NewPlayer("")
	go player.Monitor()

	if config.PlaylistPath != "" {
		loadPlaylist(config.PlaylistPath)
	}

	netEngine = fnet.DefaultEngine()
	netEngine.Encoder = fnet.JsonEncoder{} // Use json instead of protocol buffers
	netEngine.OnConnOpen = onOpenConn
	netEngine.OnConnClose = onClosedConn

	AddHandlers(netEngine)
	listen := config.Listen
	if listen == "" {
		listen = ":7447"
	}
	log.Println("Listening on", listen)
	listener := &ws.WebsocketListener{
		Engine: netEngine,
		Addr:   listen,
	}

	go CleanupLoop()
	go netEngine.AddListener(listener)
	go netEngine.ListenChannels()
	listenErrors(netEngine)
}

func AddHandlers(engine *fnet.Engine) {
	engine.AddHandler(fnet.NewHandlerSafe(handlerUserSetName, EvtSetName))
	engine.AddHandler(fnet.NewHandlerSafe(handleStatus, EvtStatus))
	engine.AddHandler(fnet.NewHandlerSafe(handlePlaylist, EvtPlaylist))
	engine.AddHandler(fnet.NewHandlerSafe(handleSettings, EvtSettings))
	engine.AddHandler(fnet.NewHandlerSafe(handleSetSettings, EvtSetSettings))
	engine.AddHandler(fnet.NewHandlerSafe(handlePlaylistClear, EvtPlaylistClear))
	engine.AddHandler(fnet.NewHandlerSafe(handlePlay, EvtPlay))
	engine.AddHandler(fnet.NewHandlerSafe(handlePause, EvtPause))
	engine.AddHandler(fnet.NewHandlerSafe(handleNext, EvtNext))
	engine.AddHandler(fnet.NewHandlerSafe(handlePrevious, EvtPrev))
	engine.AddHandler(fnet.NewHandlerSafe(handleWatchingStatusUpdate, EvtWatchingStateChange))
	engine.AddHandler(fnet.NewHandlerSafe(handleChatMessage, EvtChatMessage))
	engine.AddHandler(fnet.NewHandlerSafe(handleAuth, EvtAuth))
	engine.AddHandler(fnet.NewHandlerSafe(handleChatCmd, EvtChatCmd))
	engine.AddHandler(fnet.NewHandlerSafe(handleReloadPlaylist, EvtReloadPlaylist))
}

func loadPlaylist(path string) {
	log.Println("Started playlist loading")
	file, err := os.Open(path)

	if err != nil {
		panic(err.Error())
	}

	defer file.Close()

	reader := bufio.NewReader(file)
	scanner := bufio.NewScanner(reader)

	scanner.Split(bufio.ScanLines)

	player.Lock.Lock()
OUTER:
	for scanner.Scan() {
		for _, v := range player.CurrentPlaylist.Items {
			if v.Path == scanner.Text() {
				// Only add new items
				log.Println("Skipping", v.Path)
				continue OUTER
			}
		}

		log.Printf("Adding %s to the playlist...\n", scanner.Text())

		name := scanner.Text()
		lastIndex := strings.LastIndex(scanner.Text(), "/")
		if lastIndex != -1 {
			name = name[lastIndex:]
		}

		item := PlaylistItem{
			Kind:     ITEMTYPEMOVIE,
			Path:     scanner.Text(),
			Duration: 0,
			Title:    name,
		}
		player.CurrentPlaylist.Items = append(player.CurrentPlaylist.Items, item)
	}
	player.Lock.Unlock()
}

func LogSendError(r *http.Request, err error) {
	if err == nil {
		return
	}
	log.Printf("Error sending response to [%s] Error: %s", r.RemoteAddr, err.Error())
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

func onClosedConn(session fnet.Session) {
	name, exists := session.Data.GetString("name")
	if exists {
		viewersMutex.Lock()
		delete(viewers, name)
		viewersMutex.Unlock()
		broadcastNotification(fmt.Sprintf("%s Left :'(", name), false)
	}

	log.Println(name, " disconnected!")
	broadcastStatus()
}

func onOpenConn(session fnet.Session) {
	log.Println("Someone connected!")
	pl, err := buildPlaylistMessage()
	if err != nil {
		log.Println("Error building playlist message!: ", err)
		return
	}
	session.Conn.Send(pl)

	name := ""
	viewersMutex.Lock()
	for {
		id := <-idGenChan
		fname := fmt.Sprintf("dude#%d", id)
		_, exists := viewers[fname]
		if exists {
			continue
		} else {
			name = fname
			break
		}
	}
	session.Data.Set("name", name)
	viewers[name] = session
	viewersMutex.Unlock()

	sendNotification(session, fmt.Sprintf("Connected to fluffywatch %s!", VERSION), true)
	broadcastNotification(fmt.Sprintf("%s Joined", name), false)
	broadcastStatus()
}

func listenErrors(engine *fnet.Engine) {
	for {
		err := <-engine.ErrChan
		log.Printf("fnet Error: ", err.Error())
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
	Msg    string `json:"msg"`
	Bypass bool   `json:"bypass"` // Bypass ignore sys
}

func broadcastNotification(notification string, bypass bool) {
	n := Notification{notification, bypass}

	err := netEngine.CreateAndBroadcast(EvtNotification, n)
	if err != nil {
		log.Println("Error broadcasting notification message: ", err)
		return
	}
}

func sendNotification(session fnet.Session, text string, bypass bool) {
	n := Notification{text, bypass}

	err := netEngine.CreateAndSend(session, EvtNotification, n)
	if err != nil {
		log.Println("Error sending notification message: ", err)
		return
	}
}

func configLoader(path string) {
	ticker := time.NewTicker(1 * time.Second)
	for {
		select {
		case <-ticker.C:
			finfo, err := os.Stat(path)
			if err != nil {
				log.Println("Failed stat config", err)
				continue
			}
			if finfo.ModTime().Unix() != lastConfigLoad.Unix() {
				c, err := loadConfig(path)
				if err != nil {
					log.Println("Failed loading config..", err)
					return
				}
				configLock.Lock()
				config = c
				configLock.Unlock()
				lastConfigLoad = finfo.ModTime()
				log.Println("Loaded config")
			}
		}
	}
}

func loadConfig(path string) (*Config, error) {
	file, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var c Config
	err = json.Unmarshal(file, &c)
	return &c, err
}

func saveConfig(path string) error {
	marshalled, err := json.MarshalIndent(config, "", "\t")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, marshalled, 0664)
}

func addMod(id string) error {
	configLock.Lock()
	defer configLock.Unlock()

	for _, m := range config.Mods {
		if id == m {
			return errors.New("Allready mod")
		}
	}

	config.Mods = append(config.Mods, id)
	return saveConfig(configPath)
}

func removeMod(id string) error {
	newMods := make([]string, 0)

	configLock.Lock()
	defer configLock.Unlock()

	found := false
	for _, m := range config.Mods {
		if m != id {
			newMods = append(newMods, m)
		} else {
			found = true
		}
	}

	if !found {
		return errors.New("User not mod?")
	}
	config.Mods = newMods
	return saveConfig(configPath)
}

func banUser(id string) error {
	configLock.Lock()
	defer configLock.Unlock()
	for _, m := range config.Bans {
		if id == m {
			return errors.New("Allready banned")
		}
	}

	config.Bans = append(config.Bans, id)
	return saveConfig(configPath)
}

func unBanUser(id string) error {
	newBans := make([]string, 0)

	configLock.Lock()
	defer configLock.Unlock()

	found := false
	for _, m := range config.Bans {
		if m != id {
			newBans = append(newBans, m)
		} else {
			found = true
		}
	}
	if !found {
		return errors.New("User not banned?")
	}
	config.Bans = newBans
	return saveConfig(configPath)
}

func banIP(ip string) error {
	configLock.Lock()
	defer configLock.Unlock()

	for _, m := range config.IPBans {
		if ip == m {
			return errors.New("Allready banned")
		}
	}

	config.IPBans = append(config.IPBans, ip)
	return saveConfig(configPath)
}

func unBanIP(ip string) error {
	newBans := make([]string, 0)

	configLock.Lock()
	defer configLock.Unlock()

	found := false
	for _, m := range config.IPBans {
		if m != ip {
			newBans = append(newBans, m)
		} else {
			found = true
		}
	}
	if !found {
		return errors.New("User not banned?")
	}
	config.IPBans = newBans
	return saveConfig(configPath)
}

func CleanupLoop() {
	ticker := time.NewTicker(time.Second)

	configLock.Lock()
	segDir := config.SegmentDir
	configLock.Unlock()
	for {
		<-ticker.C
		dir, err := ioutil.ReadDir(segDir)
		if err != nil {
			log.Println("ERr cleanup:", err)
			continue
		}

		for _, v := range dir {
			split := strings.Split(v.Name(), ".")
			if len(split) < 2 {
				continue
			}

			if split[1] != "ts" {
				continue
			}

			if time.Since(v.ModTime()) > time.Second*60 {
				os.Remove(segDir + v.Name())
				//log.Println("removing", v.Name())
			}
		}
	}
}
