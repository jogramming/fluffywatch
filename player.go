package main

import (
	"errors"
	"fmt"
	"github.com/jonas747/plex"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type PlayerCMD int

const (
	PCMDSTOP PlayerCMD = iota
	PCMDNEXT
	PCMDPREV
)

const (
	ITEMTYPETV    = 0
	ITEMTYPEMOVIE = 1
)

const (
	ENDNOSUBSFOUND = 1
)

type PlaylistItem struct {
	Kind      int    `json:"kind"`      // What type of item it is (tv/movie)
	Path      string `json:"path"`      // Full path to file
	Duration  int    `json:"duration"`  // Duration in milliseconds
	Title     string `json:"title"`     // If movie, title of movie, if tv show, title of episode
	ShowTitle string `json:"showTitle"` // If tv show, title of show
	Episode   int    `json:"episode"`   // for tv
	Season    int    `json:"season"`    // for tv
}

type Playlist struct {
	Items        []PlaylistItem `json:"items"`
	CurrentIndex int            `json:"currentIndex"`
}

type TranscoderSettings struct {
	ScaleWidth int    `json:"scale"` // The width
	MaxRate    int    `json:"maxrate"`
	Preset     string `json:"preset"`
	Streams    []int  `json:"streams"`
	Seek       string `json:"seek"`
	Subs       bool   `json:"subs"`
}

type Player struct {
	Lock            sync.Mutex         `json:"-"`
	CurrentPlaylist Playlist           `json:"playlist"`
	Settings        TranscoderSettings `json:"settings"`
	Out             string             `json:"-"`
	Playing         bool               `json:"playing"`
	ManualStop      bool               `json:"manualStop"`
	Ffmpeg          *exec.Cmd          `json:"-"`
	CmdChan         chan PlayerCMD     `json:"-"`
	StartedPlaying  time.Time          `json:"-"`
	StoppedPlaying  time.Time          `json:"-"`
	StartSegment    int
}

func NewPlayer(out string) *Player {
	ts := TranscoderSettings{
		ScaleWidth: 1280,
		MaxRate:    2000,
		Preset:     "veryfast",
		Streams:    []int{0, 1},
		Subs:       true,
	}

	pl := Playlist{
		Items:        make([]PlaylistItem, 0),
		CurrentIndex: 0,
	}

	p := &Player{
		CurrentPlaylist: pl,
		Settings:        ts,
		Out:             out,
		CmdChan:         make(chan PlayerCMD),
	}
	return p
}

func (p *Player) AddPlaylistItemByPlexVideo(vid plex.PlexDirectory, kind int) error {
	if len(vid.Media) < 1 {
		return errors.New("Plex item has no media")
	}

	if len(vid.Media[0].Parts) < 1 {
		return errors.New("Plex item has no parts")
	}

	media := vid.Media[0]
	part := media.Parts[0]

	duration, _ := strconv.Atoi(part.Duration)
	ep, _ := strconv.Atoi(vid.Index)
	season, _ := strconv.Atoi(vid.ParentIndex)

	pi := PlaylistItem{
		Kind:      kind,
		Path:      part.File,
		Duration:  duration,
		Title:     vid.Title,
		ShowTitle: vid.GrandparentTitle,
		Episode:   ep,
		Season:    season,
	}

	log.Println("Appending to playlist")
	log.Println(pi)

	p.Lock.Lock()
	p.CurrentPlaylist.Items = append(p.CurrentPlaylist.Items, pi)
	p.Lock.Unlock()

	return nil
}

func (p *Player) AddPlaylistItem(item PlaylistItem) error {
	log.Println("Appending to playlist")
	log.Println(item.Path)

	p.Lock.Lock()
	p.CurrentPlaylist.Items = append(p.CurrentPlaylist.Items, item)
	p.Lock.Unlock()

	return nil
}

func (p *Player) Play() {
	if p.Playing {
		log.Println("Tried playing when were allready playing...")
		return
	}

	p.Playing = true
	defer func() {
		p.Playing = false
		broadcastPlaylistStatus()
	}()
	for {
		p.ManualStop = false
		if p.CurrentPlaylist.CurrentIndex >= len(p.CurrentPlaylist.Items) {
			// At the end of the playlist
			p.Lock.Lock()
			p.CurrentPlaylist.CurrentIndex = 0
			p.Lock.Unlock()
			broadcastPlaylistStatus()
			return
		}

		if p.CurrentPlaylist.CurrentIndex < 0 {
			p.Lock.Lock()
			p.CurrentPlaylist.CurrentIndex = 0
			p.Lock.Unlock()
		}

		p.Lock.Lock()
		startSeg := p.StartSegment
		p.StartSegment += 1000
		p.Lock.Unlock()

		item := p.CurrentPlaylist.Items[p.CurrentPlaylist.CurrentIndex]
		// Validate the path
		err := ValidatePath(item.Path)
		if err != nil {
			// Path is invalid, skip
			p.Lock.Lock()
			p.CurrentPlaylist.CurrentIndex++
			p.Lock.Unlock()
			log.Println("Invalid path skipping element")
			broadcastPlaylistStatus()
			continue
		}

		// Actually start playing the item
		reason := p.PlayItem(item, p.Settings.Subs, startSeg)
		if reason == ENDNOSUBSFOUND {
			log.Println("Falling back to no subs")
			p.PlayItem(item, false, startSeg)
		}

		// Reset the seek
		p.Lock.Lock()
		p.Settings.Seek = ""
		p.StoppedPlaying = time.Now()
		if p.ManualStop {
			// Stop playback if there was a manual stop
			// also set the seek to wherever we were -5 seconds to make sure we dont miss anything
			duration := p.StoppedPlaying.Sub(p.StartedPlaying)
			duration -= time.Duration(3) * time.Second
			seconds := int(duration.Seconds())
			if seconds > 0 {
				stringed := StringLocation(int(duration.Seconds()))
				p.Settings.Seek = stringed
			}

			p.Lock.Unlock()
			broadcastPlaylistStatus()
			return
		}

		// Continue on with the next item in the playlist
		p.CurrentPlaylist.CurrentIndex++
		p.Lock.Unlock()
		broadcastPlaylistStatus()
	}
}

func escapeFilters(in string) string {
	replacer := strings.NewReplacer("[", "\\[", "]", "\\]")
	return replacer.Replace(in)
}

func (p *Player) PlayItem(item PlaylistItem, subsEnabled bool, startSeg int) int {
	p.Lock.Lock()
	p.StartedPlaying = time.Now()
	p.Lock.Unlock()

	globalArgs := []string{
		//"-report",
		"-re",
	}

	// If were seeking append that argument
	seek := p.Settings.Seek
	if seek != "" {

		th, tm, ts := ParseLocationStr(seek)
		tm += th * 60
		ts += tm * 60

		if ts > 0 {
			globalArgs = append(globalArgs, "-ss", seek)

			p.Lock.Lock()
			p.StartedPlaying = p.StartedPlaying.Add(time.Duration(ts) * time.Second * -1)
			p.Lock.Unlock()
		}
	}
	// Broadcast to new status
	broadcastStatus()

	inputArgs := []string{
		"-i", item.Path,
	}

	// Map the streams
	for _, s := range p.Settings.Streams {
		inputArgs = append(inputArgs, "-map")
		inputArgs = append(inputArgs, fmt.Sprintf("0:%d", s))
	}

	vf := fmt.Sprintf("scale=%d:trunc(ow/a/2)*2", p.Settings.ScaleWidth)
	if subsEnabled {
		vf += fmt.Sprintf(",subtitles=%s", escapeFilters(item.Path))
	}
	log.Println("Filters: ", vf)

	configLock.Lock()
	playlistPath := config.HLSPlaylistPath
	//segDir := config.SegmentDir
	configLock.Unlock()

	miscArgs := []string{
		"-strict", "-2", // Enable experimental codecs
		// "-c:a", "libfdk_aac", // Audio codec
		"-c:a", "aac", // Audio codec
		"-ar", "44100", // Audio freq
		"-vbr", "5", // Audio variable bitrate (5 is highest)
		"-c:v", "libx264", // Video codec
		"-profile:v", "baseline", // h264 profile
		"-preset", p.Settings.Preset, // x264 preset
		"-maxrate", fmt.Sprintf("%dk", p.Settings.MaxRate),
		"-bufsize", fmt.Sprintf("%dk", p.Settings.MaxRate*2),
		"-vf", vf, // Set scale
		"-x264-params", "keyint=100:no-scenecut=1", // Set scale
		"-f", "hls", // Set scale
		"-start_number", fmt.Sprint(startSeg),
		"-hls_allow_cache", "0",
		"-hls_flags", "discont_start",
		//"-reset_timestamps", "1",
		// "-segment_start_number", fmt.Sprint(startSeg),
		// "-segment_list_flags", "live",
		// "-segment_list", playlistPath,
		// "-segment_list_size", "10",
	}

	// Set output
	args := make([]string, 0)
	args = append(args, globalArgs...)
	args = append(args, inputArgs...)
	args = append(args, miscArgs...)

	// Set output path
	args = append(args, playlistPath)
	log.Println(args)

	//Finally execute the command
	p.Ffmpeg = exec.Command("ffmpeg", args...)

	output, err := p.Ffmpeg.CombinedOutput()
	if err != nil {
		log.Println("ERROR:", err)
	}

	log.Println(string(output))
	log.Println("Ended FFMPEG")

	if subsEnabled && strings.Contains(string(output), "Error initializing filter 'subtitles' with args") {
		return ENDNOSUBSFOUND
	}

	return 0
}

func (p *Player) Monitor() {
	log.Println("FFMonitor started")
	for {
		select {
		case c := <-p.CmdChan:
			p.Lock.Lock()
			switch c {
			case PCMDSTOP:
				if p.Playing {
					if p.Ffmpeg != nil && p.Ffmpeg.Process != nil {
						p.Ffmpeg.Process.Signal(os.Interrupt)
						p.ManualStop = true
					}
				}
			case PCMDNEXT:
				p.Settings.Seek = ""
				if !p.Playing {
					p.CurrentPlaylist.CurrentIndex++
				} else {
					if p.Ffmpeg != nil && p.Ffmpeg.Process != nil {
						p.Ffmpeg.Process.Signal(os.Interrupt)
					}
				}
			case PCMDPREV:
				p.Settings.Seek = ""
				if !p.Playing {
					p.CurrentPlaylist.CurrentIndex--
				} else {
					p.CurrentPlaylist.CurrentIndex -= 2
					if p.Ffmpeg != nil && p.Ffmpeg.Process != nil {
						p.Ffmpeg.Process.Signal(os.Interrupt)
					}
				}
			}
			p.Lock.Unlock()
		}
	}
	log.Println("FFMonitor stopped")
}

func ParseLocationStr(str string) (h, m, s int) {
	split := strings.Split(str, ":")
	if len(split) >= 3 {
		h, _ = strconv.Atoi(split[0])
		m, _ = strconv.Atoi(split[1])
		s, _ = strconv.Atoi(split[2])
	}
	return
}

func StringLocation(s int) string {
	h := (s / 60) / 60
	m := (s / 60) % 60
	ss := s % 60
	return fmt.Sprintf("%d:%d:%d", h, m, ss)
}
