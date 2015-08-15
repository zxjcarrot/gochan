package gochan

import (
	"encoding/binary"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestNewChanNet(t *testing.T) {
	conn, err := net.Dial("tcp", "www.example.com:80")
	if err != nil {
		t.Fatal("failed to connect to www.example.com :", err)
	}
	log.Println("connected")
	rc, wc := NewChan(conn, 50, 50, 512)

	wc <- []byte("GET / HTTP/1.0\r\nhost:www.example.com\r\n\r\n")
	for cd := range rc {
		log.Println(string(cd.Data), cd.Err)
	}
	CloseWriteChan(wc)
	conn.Close()
}

func TestNewReadonlyChanNet(t *testing.T) {
	conn, err := net.Dial("tcp", "www.example.com:80")
	if err != nil {
		t.Fatal("failed to connect to www.example.com :", err)
	}
	log.Println("connected")
	rc := NewReadonlyChan(conn, 50, 512)

	n, err := conn.Write([]byte("GET / HTTP/1.0\r\nhost:www.example.com\r\n\r\n"))
	if n != len([]byte("GET / HTTP/1.0\r\nhost:www.example.com\r\n\r\n")) || err != nil {
		t.Fatal("failed write request: ", err)
	}
	for cd := range rc {
		log.Println(string(cd.Data), cd.Err)
	}
	conn.Close()
}

var want = ""

func TestNewChanWriteFile(t *testing.T) {
	f, err := os.OpenFile("./testfile", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0660)
	if err != nil {
		t.Fatal("failed to open ./testfile :", err)
	}

	wc := NewWriteonlyChan(f, 50)

	for i := 0; i < 10; i = i + 1 {
		want += "eat my shorts" + strconv.Itoa(i) + "!!!\n"
		wc <- []byte("eat my shorts" + strconv.Itoa(i) + "!!!\n")
	}
	CloseWriteChan(wc)
	f.Close()

	log.Println(want)
}

func TestNewChanReadFile(t *testing.T) {
	f, err := os.Open("./testfile")
	if err != nil {
		t.Fatal("failed to open ./testfile :", err)
	}
	rc := NewReadonlyChan(f, 50, 4096)

	defer f.Close()
	for cd := range rc {
		s := string(cd.Data)
		log.Println(s, cd.Err)
		if want != s && cd.Err == nil {
			t.Fatalf("want [%s], get [%s], len: %d %d", want, s, len(want), len(s))
		}
	}
}

func TestGochanPipe(t *testing.T) {
	rf, wf, err := os.Pipe()
	if err != nil {
		t.Fatal("failed to create pipe:", err)
	}
	rc := NewReadonlyChan(rf, 50, 4096)
	wc := NewWriteonlyChan(wf, 50)
	done := make(chan bool)

	go func() {
		for i := 0; i < 10; i = i + 1 {
			wc <- []byte("eat my shorts" + strconv.Itoa(i) + "!!!\n")
		}
		CloseWriteChan(wc)
		wf.Close()
		done <- true
	}()

	var get = ""
	go func() {
		for cd := range rc {
			s := string(cd.Data)
			get += s
			log.Print(s, cd.Err)
		}
		done <- true
	}()

	_ = <-done
	_ = <-done

	if want != get {
		t.Fatalf("want [%s], get [%s], len: %d %d", want, get, len(want), len(get))
	}
}

func TestGochanExec(t *testing.T) {
	cmd := exec.Command("ls", "-R", "/")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	rc := NewReadonlyChan(stdout, 0, 4096)
	timeout := time.After(1 * time.Second)
loop:
	for {
		select {
		case cd, ok := <-rc:
			if !ok || cd.Err == io.EOF {
				break loop
			}
			log.Print(string(cd.Data))
		case <-timeout:
			t.Log("sleep took too long")
			cmd.Process.Kill()
			break loop
		}
	}
	CloseReadChan(rc)
}

func TestGochanPipeError(t *testing.T) {
	rf, wf, err := os.Pipe()
	if err != nil {
		t.Fatal("failed to create pipe:", err)
	}
	wc := NewWriteonlyChan(wf, 50)
	done := make(chan bool)
	rf.Close()

	want = ""
	go func() {
		wc <- []byte("eat my shorts!!!\n")
		CloseWriteChan(wc)
		done <- true
	}()

	_ = <-done
}

func TestModifyReadSize(t *testing.T) {
	rf, wf, err := os.Pipe()
	if err != nil {
		t.Fatal("failed to create pipe:", err)
	}
	var wantData = []string{
		"monkeys",
		"are",
		"typing",
		"randomly",
	}

	var wantDataLen = []uint32{
		uint32(len(wantData[0])),
		uint32(len(wantData[1])),
		uint32(len(wantData[2])),
		uint32(len(wantData[3])),
	}

	for i := 0; i < len(wantData); i = i + 1 {
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, wantDataLen[i])
		wf.Write(b)
		wf.Write([]byte(wantData[i]))
	}
	wf.Close()

	headerSize := 4
	rc := NewReadonlyChan(rf, -1, uint32(headerSize))
	i := 0
	for {
		//after reading the header, rc will waits for ModiyReadSize to tell it the next read size
		cd := <-rc
		if cd.Err == io.EOF {
			break
		} else if cd.Err != nil || len(cd.Data) != headerSize {
			t.Fatal("failed to read header size", cd)
		}

		payloadSize := binary.BigEndian.Uint32(cd.Data)
		// tell rc the next read size should be payloadSize
		if err := ModiyReadSize(rc, payloadSize); err != nil {
			t.Fatal("failed to modify read size", err)
		}
		// read payload
		cd = <-rc
		if cd.Err != nil {
			t.Fatal("failed to read data", cd)
		}
		log.Printf("payload size: %d, payload: %s\n", payloadSize, string(cd.Data))

		if wantDataLen[i] != payloadSize || string(cd.Data) != wantData[i] {
			t.Fatalf("want data len: %d, want data: %s, got data len: %d, data: %s",
				wantDataLen[i], wantData[i], payloadSize, string(cd.Data))
		}

		// now tell rc to read headerSize bytes of data again
		if err := ModiyReadSize(rc, uint32(headerSize)); err != nil {
			t.Fatal("failed to modify read size", err)
		}
		i = i + 1
	}

	err = ModiyReadSize(make(chan ChanData, 0), 1)
	if err == nil {
		t.Fatal("failed")
	}
	log.Print(err)
}
