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
	"io"
	"log"
	"sync"
	"sync/atomic"
)

// ChanData contains data and error(if any) during transmission
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
	rw       io.ReadWriter
	readSize uint32
}

var (
	mtx sync.Mutex
	rcm map[<-chan ChanData]*goChan
	wcm map[chan<- []byte]*goChan
)

func init() {
	log.SetFlags(log.Flags() | log.Lmicroseconds)
	rcm = make(map[<-chan ChanData]*goChan)
	wcm = make(map[chan<- []byte]*goChan)
}

func readChan(gc *goChan) {
	defer func() {
		_ = recover()
	}()
	defer CloseReadChan(gc.rc)

	for {
		readsize := atomic.LoadUint32(&gc.readSize)
		log.Printf("about to read %d bytes, %p\n", readsize, &gc.readSize)
		b := make([]byte, readsize)
		n, err := gc.rw.Read(b)
		b = b[0:n]
		if err == io.EOF || err == io.ErrClosedPipe {
			gc.rc <- ChanData{b, err}
			break
		} else {
			gc.rc <- ChanData{b, err}
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
		_, err := gc.rw.Write(s)
		if err != nil {
			log.Printf("write error %v\n", err)
			gc.done <- empty{} // work is done
			break
		}
	}
}

// NewChan creates two channels for reading and writing.
// The reading channel is typed by ChanData struct, while the writing channel is typed by []byte.
// @rw: anything that implements io.ReadWriter interface.
// @rcBufsize: the buffer size of the reading channel, a unbuffered channel will be created if 0 provided.
// @wcBufsize: the buffer size of the writing channel, a unbuffered channel will be created if 0 provided.
// @readSize: the read size for io.Read() method, it might return less bytes.
// Every read operation on the channel will return a ChanData containing data and error if any,
// the actual bytes read can be obtained from len(ChanData.Data).
// Every write operation should provided a ChanData containing a slice of bytes and
// the Err field in the ChanData struct will be ignored.
func NewChan(rw io.ReadWriter, rcBufsize uint, wcBufsize, readSize uint32) (<-chan ChanData, chan<- []byte) {
	mtx.Lock()
	defer mtx.Unlock()
	var gc = goChan{make(chan ChanData, rcBufsize), make(chan []byte, wcBufsize), make(chan empty, 1), rw, readSize}
	rcm[gc.rc] = &gc
	wcm[gc.wc] = &gc
	go readChan(&gc)
	go writeChan(&gc)
	return gc.rc, gc.wc
}

// NewReadonlyChan creates a readonly channel typed by ChanData struct.
// see comments on NewChan() method for details on parameters.
func NewReadonlyChan(rw io.ReadWriter, rcBufsize uint, readSize uint32) <-chan ChanData {
	mtx.Lock()
	defer mtx.Unlock()
	var gc = goChan{rc: make(chan ChanData, rcBufsize), done: make(chan empty, 1), rw: rw, readSize: readSize}
	rcm[gc.rc] = &gc
	go readChan(&gc)
	return gc.rc
}

// NewWriteonlyChan creates a writeonly channel typed by []byte.
// see comments on NewChan() method for details on parameters.
func NewWriteonlyChan(rw io.ReadWriter, wcBufsize uint) chan<- []byte {
	mtx.Lock()
	defer mtx.Unlock()
	var gc = goChan{wc: make(chan []byte, wcBufsize), done: make(chan empty, 1), rw: rw}
	go writeChan(&gc)
	wcm[gc.wc] = &gc
	return gc.wc
}

// CloseWriteChan closes the writing channel.
// it blocks until the writing channel finished writing its
// buffered data or encountered errors.
// @wc: the writing side channel
func CloseWriteChan(wc chan<- []byte) error {
	mtx.Lock()
	defer mtx.Unlock()
	if gc, ok := wcm[wc]; ok {
		delete(wcm, wc)
		close(wc)
		_ = <-gc.done // wait for works to be done
	}

	return nil
}

// CloseReadChan closes the reading channel.
// @wc: the reading side channel
func CloseReadChan(rc <-chan ChanData) error {
	mtx.Lock()
	defer mtx.Unlock()
	if gc, ok := rcm[rc]; ok {
		delete(rcm, rc)
		close(gc.rc)
	}
	return nil
}
