package main

import (
	"errors"
	"fmt"
	"github.com/jonas747/plex"
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
	CurrentPlaylist Playlist           `json:"playlist"`
	Settings        TranscoderSettings `json:"settings"`
	Out             string             `json:"-"`
	Playing         bool               `json:"playing"`
	ManualStop      bool               `json:"manualStop"`
	Ffmpeg          *exec.Cmd          `json:"-"`
	CmdChan         chan PlayerCMD     `json:"-"`
	StartedPlaying  time.Time          `json:"-"`
	StoppedPlaying  time.Time          `json:"-"`
	Lock            sync.Mutex         `json:"-"`
}

func NewPlayer(out string) *Player {
	ts := TranscoderSettings{
		ScaleWidth: 1280,
		MaxRate:    2000,
		Preset:     "veryfast",
		Streams:    []int{0, 1},
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

	fmt.Println("Appending to playlist")
	fmt.Println(pi)

	p.Lock.Lock()
	p.CurrentPlaylist.Items = append(p.CurrentPlaylist.Items, pi)
	p.Lock.Unlock()

	return nil
}

func (p *Player) Play() {
	if p.Playing {
		fmt.Println("Tried playing when were allready playing...")
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

		item := p.CurrentPlaylist.Items[p.CurrentPlaylist.CurrentIndex]
		// Validate the path
		err := ValidatePath(item.Path)
		if err != nil {
			// Path is invalid, skip
			p.Lock.Lock()
			p.CurrentPlaylist.CurrentIndex++
			p.Lock.Unlock()
			fmt.Println("Invalid path skipping element")
			broadcastPlaylistStatus()
			continue
		}

		// Actually start playing the item
		p.PlayItem(item)

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

func (p *Player) PlayItem(item PlaylistItem) {
	p.Lock.Lock()
	p.StartedPlaying = time.Now()
	p.Lock.Unlock()

	globalArgs := []string{
		"-report",
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
	if p.Settings.Subs {
		vf += fmt.Sprintf(",subtitles=%s", escapeFilters(item.Path))
	}
	fmt.Println("Filters: ", vf)

	miscArgs := []string{
		"-strict", "-2", // Enable experimental codecs
		"-c:a", "libfdk_aac", // Audio codec
		"-ar", "44100", // Audio freq
		"-vbr", "5", // Audio variable bitrate (5 is highest)
		"-c:v", "libx264", // Video codec
		"-profile:v", "baseline", // h264 profile
		"-preset", p.Settings.Preset, // x264 preset
		"-maxrate", fmt.Sprintf("%dk", p.Settings.MaxRate),
		"-bufsize", fmt.Sprintf("%dk", p.Settings.MaxRate*2),
		"-vf", vf, // Set scale
		"-f", "flv",
	}

	// // enable experimental codecs
	// args = append(args, "-strict")
	// args = append(args, "-2")

	// // Set audio codec
	// args = append(args, "-c:a")
	// args = append(args, "libfdk_aac")

	// // Set audio freq
	// args = append(args, "-ar")
	// args = append(args, "44100")

	// // Set audio variable bitrate
	// args = append(args, "-vbr")
	// args = append(args, "5") // 5 is highest quality

	// // Set Video codec
	// args = append(args, "-c:v")
	// args = append(args, "libx264")

	// // Set baseline profile
	// args = append(args, "-profile:v")
	// args = append(args, "baseline")

	// // Set preset
	// args = append(args, "-preset")
	// args = append(args, p.Settings.Preset)

	// // Set max rate
	// args = append(args, "-maxrate")
	// args = append(args, fmt.Sprintf("%dk", p.Settings.MaxRate))

	// // Set bufsize
	// args = append(args, "-bufsize")
	// args = append(args, fmt.Sprintf("%dk", p.Settings.MaxRate*2))

	// // Set video scale
	// args = append(args, "-vf")
	// args = append(args, fmt.Sprintf("scale=%d:trunc(ow/a/2)*2", p.Settings.ScaleWidth))

	// // Set format to flv
	// args = append(args, "-f")
	// args = append(args, "flv")

	// Set output
	args := make([]string, 0)
	args = append(args, globalArgs...)
	args = append(args, inputArgs...)
	args = append(args, miscArgs...)

	// Set output path
	args = append(args, p.Out)

	//Finally execute the command
	p.Ffmpeg = exec.Command("ffmpeg", args...)

	output, err := p.Ffmpeg.CombinedOutput()
	if err != nil {
		fmt.Println("ERROR:", err)
	}

	fmt.Println(string(output))
	fmt.Println("Ended FFMPEG")
}

func (p *Player) Monitor() {
	fmt.Println("FFMonitor started")
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
	fmt.Println("FFMonitor stopped")
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
