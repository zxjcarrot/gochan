// gochan package is a wrapper around io.ReadWriter interface
// to create channels for communication. gochan does this by
// encapsulating io.ReadWriter interface into two separate channels
// for reading and writing.
//
// Data read from the reading channel is  wrapped in the ChanData struct
// which also contains a Err field if there is any error during reading.
// Data can be feed into the writing channel as a slice of bytes directly
// without any wrapping.
//
// When done with gochan, simply close the writing channel through CloseWriteChan()
// method or by closing  the io.ReadWriter interface.
// You should note that closing the writing channel won't affect the reading channel,
// but closing the io.ReadWriter interface will causes both reading and writing channels to be closed.
// Note:
//      1. If you closed io.ReadWriter interface and the writing channel
//         still has buffered data at the point, these data will be lost.To prevent this,
//         use CloseWriteChan() method. CloseWriteChan() will block until the
//         writing channel has finished writing buffered data or encountered errors.
//         After then, you can safely close the io.ReadWriter interface.
//      2. The reading channel won't be closed since it blocks at reading the data
//         from the io.ReadWriter interface. So the cleanest way to cleanup both
//         reading and writing channels is using CloseWriteChan() method followed by
//         a Close() method of the underlying io.ReadWriter interface if any.
//
// Internally, gochan uses two goroutines for reading and writing.
// The reading goroutine will exit when io.EOF or io.ErrClosedPipe is encountered.
// The writing goroutine will exit when there is any error during the writing.
package gochan

import (
	"errors"
	"io"
	"log"
	"os"
	"sync"
)

// ChanData contains data and error(if any) for transmission
type ChanData struct {
	Data []byte
	Err  error
}

type empty struct{}

// goChan is a internal struct to represent a proxy between clients and io(network, filesystem etc...)
type goChan struct {
	rc       chan ChanData
	wc       chan []byte
	done     chan empty
	sizec    chan uint32 // channel to modify read channel's read size
	r        io.Reader
	w        io.Writer
	readSize uint32
}

var (
	mtx    sync.Mutex
	rcm    = make(map[<-chan ChanData]*goChan)
	wcm    = make(map[chan<- []byte]*goChan)
	logger = log.New(os.Stderr, "", log.Flags()|log.Lmicroseconds)
)

func readChan(gc *goChan) {
	defer func() {
		_ = recover()
	}()
	defer CloseReadChan(gc.rc)
	for {
		b := make([]byte, gc.readSize)
		n, err := gc.r.Read(b)
		b = b[0:n]
		gc.rc <- ChanData{b, err}
		if err == io.EOF || err == io.ErrClosedPipe {
			break
		}

		if gc.sizec != nil { // block till the new readsize comes in
			gc.readSize = <-gc.sizec
		}
	}
}

func writeChan(gc *goChan) {
	defer CloseWriteChan(gc.wc)
	for {
		s, ok := <-gc.wc
		if !ok { // client closed the channel
			gc.done <- empty{} // work is done
			break
		}
		_, err := gc.w.Write(s)
		if err != nil {
			logger.Printf("write error %v\n", err)
			gc.done <- empty{} // work is done
			break
		}
	}
}

// NewChan creates two separate channels with buffer size rcBufsize and wcBufsize for reading/writing from/to rw.
// Unbuffered channels will be created if buffer size 0 provided.
// Especially a negative read channel buffer size will create a unbuffered channel with readSize modification support,
// e.g.:
//		headerSize := 4
//      // a negative -1 read buffer size will create a unbuffered channel which only reads data once after creation
//      // or ModifyReadSize function being called
//      rc, _ := gochan.NewChan(rw, -1, 10, headerSize)
//      for {
//			cd := <- rc
//			if cd.Err != nil || len(cd.Data) != 4 {
//	 			// handle errror...
//			}
//          dataSize := binary.BigEndian.Uint32(cd.Data)
//			// now make it read a payload size of data
//			gochan.ModifyReadSize(rc, dataSize)
//          // read the payload
//          cd = <- rc
//          // make it read a header size of data again
//          gochan.ModifyRedSize(rc, headerSize)
//          // do what you want with the payload
//		}
//
// The readSize parameter indicates the number of bytes a io.Reader.Read() operation will perform.
// The reading channel returned is typed by ChanData struct, while the writing one is typed by []byte.
// Every read operation on the channel will return a ChanData containing data and error if any,
// the actual bytes read can be obtained from len(ChanData.Data).
// Every write operation should provided a ChanData containing a slice of bytes and
// the Err field in the ChanData struct will be ignored.
func NewChan(rw io.ReadWriter, rcBufsize int, wcBufsize uint, readSize uint32) (<-chan ChanData, chan<- []byte) {
	mtx.Lock()
	defer mtx.Unlock()
	var gc goChan

	if rcBufsize < 0 {
		gc = goChan{
			make(chan ChanData, 0),
			make(chan []byte, wcBufsize),
			make(chan empty, 1),
			make(chan uint32, 1),
			rw, rw,
			readSize,
		}
	} else {
		gc = goChan{
			rc:   make(chan ChanData, rcBufsize),
			wc:   make(chan []byte, wcBufsize),
			done: make(chan empty, 1),
			r:    rw, w: rw,
			readSize: readSize,
		}
	}

	rcm[gc.rc] = &gc
	wcm[gc.wc] = &gc
	go readChan(&gc)
	go writeChan(&gc)
	return gc.rc, gc.wc
}

// NewReadonlyChan creates a readonly channel typed by ChanData struct.
// see comments on NewChan() method for details on parameters.
func NewReadonlyChan(r io.Reader, rcBufsize int, readSize uint32) <-chan ChanData {
	mtx.Lock()
	defer mtx.Unlock()
	var gc goChan

	if rcBufsize < 0 {
		gc = goChan{
			rc:       make(chan ChanData, 0),
			done:     make(chan empty, 1),
			sizec:    make(chan uint32, 1),
			r:        r,
			readSize: readSize,
		}
	} else {
		gc = goChan{
			rc:       make(chan ChanData, rcBufsize),
			done:     make(chan empty, 1),
			r:        r,
			readSize: readSize,
		}
	}

	rcm[gc.rc] = &gc
	go readChan(&gc)
	return gc.rc
}

// NewWriteonlyChan creates a writeonly channel typed by []byte.
// see comments on NewChan() method for details on parameters.
func NewWriteonlyChan(w io.Writer, wcBufsize uint) chan<- []byte {
	mtx.Lock()
	defer mtx.Unlock()
	var gc = goChan{wc: make(chan []byte, wcBufsize), done: make(chan empty, 1), w: w}
	go writeChan(&gc)
	wcm[gc.wc] = &gc
	return gc.wc
}

// CloseWriteChan closes the writing channel wc.
// it blocks until the writing channel finished writing its
// buffered data or encountered errors.
func CloseWriteChan(wc chan<- []byte) error {
	mtx.Lock()
	if gc, ok := wcm[wc]; ok {
		delete(wcm, wc)
		mtx.Unlock()
		close(wc)
		_ = <-gc.done // wait for works to be done
		return nil
	}
	mtx.Unlock()
	return nil
}

// CloseReadChan closes the reading channel rc.
func CloseReadChan(rc <-chan ChanData) error {
	mtx.Lock()
	if gc, ok := rcm[rc]; ok {
		delete(rcm, rc)
		mtx.Unlock()
		close(gc.rc)
		return nil
	}

	mtx.Unlock()
	return nil
}

// ModiyReadSize takes a read channel and a new read size.
// it modify the read channel's read size of next read operation.
// it return error if rc is closed or not a read channel from the gochan package.
func ModiyReadSize(rc <-chan ChanData, newSize uint32) error {
	mtx.Lock()
	if gc, ok := rcm[rc]; ok {
		mtx.Unlock()
		gc.sizec <- newSize
		return nil
	}
	return errors.New("read channel does not exist")
}
