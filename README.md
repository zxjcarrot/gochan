# Gochan
Gochan turns `io.ReadWriter` interface into channels. Gochan does this by
encapsulating `io.ReadWriter` interface into two separate channels
for reading and writing.  

# Data
Data read from the reading channel is  wrapped in the `ChanData` struct
which also contains a Err field if there is any error during reading.
Data can be feed into the writing channel as `[]byte` directly
without any wrapping.  

# Cleanup
When done with gochan, simply close the writing channel through `CloseWriteChan()` method or by closing  the `io.ReadWriter` interface.
Closing the writing channel won't affect the reading channel,
but closing the `io.ReadWriter` interface causes both reading and writing channels to be closed.    
Note:  
1. If you closed `io.ReadWriter` interface and the writing channel
still has buffered data at the point, these data will be lost. To prevent this, use `CloseWriteChan()` method. `CloseWriteChan()` will block until the writing channel has finished writing buffered data or encountered errors. After then, you can safely close the `io.ReadWriter` interface.  
2. The reading channel won't be closed since it blocks at reading the data from the `io.ReadWriter` interface. So the cleanest way to cleanup both reading and writing channels is using `CloseWriteChan()` method followed by a `Close()` method of the underlying io.ReadWriter interface if any.  


# Examples
Socket:
```go
conn, err := net.Dial("tcp", "www.example.com:80")
if err != nil {
	t.Fatal("failed to connect to www.example.com :", err)
}
log.Println("connected")
// creates a read channel with read size 512 bytes and buffer size 50,
// and a write channel with buffer size 50
rc, wc := gochan.NewChan(conn, 50, 50, 512)

wc <- []byte("GET / HTTP/1.0\r\nhost:www.example.com\r\n\r\n")
// loop until EOF
for cd := range rc {
    // cd contains data and error if any
    // len(cd.Data) returns the actual data read
	log.Println(string(cd.Data), cd.Err)
}

// closing connection will automatically close two channels
conn.Close()
```
Write-only channel:
```go
f, err := os.OpenFile("./testfile", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0660)
if err != nil {
	t.Fatal("failed to open ./testfile :", err)
}
// create a write-only channel with buffer size 50
wc := gochan.NewWriteonlyChan(f, 50)

for i := 0; i < 10; i = i + 1 {
	want += "eat my shorts" + strconv.Itoa(i) + "!!!\n"
	wc <- []byte("eat my shorts" + strconv.Itoa(i) + "!!!\n")
}
// CloseWriteChan will blocks until the buffered data in the channel is written into the connection.
gochan.CloseWriteChan(wc)
f.Close()

log.Println(want)

```
Read-only channel:
```go
f, err := os.Open("./testfile")
if err != nil {
	t.Fatal("failed to open ./testfile :", err)
}
// create a read-only channel with buffer size 50 and read size 4096
rc := gochan.NewReadonlyChan(f, 50, 4096)

defer f.Close()
for cd := range rc {
	s := string(cd.Data)
	log.Println(s, cd.Err)
}
```
Timed command execution:
```go
cmd := exec.Command("ls", "-R", "/")
stdout, err := cmd.StdoutPipe()
if err != nil {
	t.Fatal(err)
}
if err := cmd.Start(); err != nil {
	t.Fatal(err)
}
rc := gochan.NewReadonlyChan(stdout, 0, 4096)
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
gochan.CloseReadChan(rc)
```
Pipe:
```go
rf, wf, err := os.Pipe()
if err != nil {
	t.Fatal("failed to create pipe:", err)
}
rc := gochan.NewReadonlyChan(rf, 50, 4096)
wc := gochan.NewWriteonlyChan(wf, 50)
done := make(chan bool)

go func() {
	for i := 0; i < 10; i = i + 1 {
		wc <- []byte("eat my shorts" + strconv.Itoa(i) + "!!!\n")
	}
	gochan.CloseWriteChan(wc)
	wf.Close()
	done <- true
}()

go func() {
	for cd := range rc {
		s := string(cd.Data)
		log.Print(s, cd.Err)
	}
	done <- true
}()

_ = <-done
_ = <-done
```

Modify read size after the creation of channels:
```go
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
// -1 read buffer size creates a readSize-modifiable channel
rc := gochan.NewReadonlyChan(rf, -1, uint32(headerSize))
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
	if err := gochan.ModiyReadSize(rc, payloadSize); err != nil {
		t.Fatal("failed to modify read size", err)
	}
	// read payload
	cd = <-rc
	if cd.Err != nil {
		t.Fatal("failed to read data", cd)
	}
	log.Printf("payload size: %d, payload: %s\n", payloadSize, string(cd.Data))
	// now tell rc to read headerSize bytes of data again
	if err := gochan.ModiyReadSize(rc, uint32(headerSize)); err != nil {
		t.Fatal("failed to modify read size", err)
	}
}
```
see `gochan_test.go` for complete code.