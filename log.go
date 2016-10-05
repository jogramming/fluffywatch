package main

import (
	"os"
)

type Logger struct {
	// file
	file      *os.File
	writeChan chan []byte
}

func newLogger() *Logger {
	file, err := os.OpenFile("fluffy.log", os.O_RDWR|os.O_APPEND|os.O_CREATE, 0660)
	if err != nil {
		panic(err)
	}

	return &Logger{
		file:      file,
		writeChan: make(chan []byte, 10),
	}
}

func (l *Logger) Write(p []byte) (n int, err error) {
	os.Stdout.Write(p)
	l.file.Write(p)
	return len(p), nil
}

func (l *Logger) Writer() {}
