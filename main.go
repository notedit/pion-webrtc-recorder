package main

import (
	"fmt"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/notedit/rtmp/format/flv"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v2"
	"io"
	"os"
)

var startBytes = []byte{0x00, 0x00, 0x00, 0x01}

type RTPJitter struct {
	clockrate    uint32
	cap          uint16
	packetsCount uint32
	nextSeqNum   uint16
	packets      []*rtp.Packet
	packetsSeqs  []uint16

	lastTime uint32
	nextTime uint32

	maxWaitTime uint32
	clockInMS   uint32
}

// cap maybe 512 or 1024 or more
func NewJitter(cap uint16, clockrate uint32) *RTPJitter {
	jitter := &RTPJitter{}
	jitter.packets = make([]*rtp.Packet, cap)
	jitter.packetsSeqs = make([]uint16, cap)
	jitter.cap = cap
	jitter.clockrate = clockrate
	jitter.clockInMS = clockrate / 1000
	jitter.maxWaitTime = 100
	return jitter
}

func (self *RTPJitter) Add(packet *rtp.Packet) bool {

	idx := packet.SequenceNumber % self.cap
	self.packets[idx] = packet
	self.packetsSeqs[idx] = packet.SequenceNumber

	if self.packetsCount == 0 {
		self.nextSeqNum = packet.SequenceNumber - 1
		self.nextTime = packet.Timestamp
	}

	self.lastTime = packet.Timestamp
	self.packetsCount++
	return true
}

func (self *RTPJitter) SetMaxWaitTime(wait uint32) {
	self.maxWaitTime = wait
}

func (self *RTPJitter) GetOrdered() (out []*rtp.Packet) {
	nextSeq := self.nextSeqNum + 1
	for {
		idx := nextSeq % self.cap
		if self.packetsSeqs[idx] != nextSeq {
			// if we reach max wait time
			if (self.lastTime - self.nextTime) > self.maxWaitTime*self.clockInMS {
				nextSeq++
				continue
			}
			break
		}
		packet := self.packets[idx]
		out = append(out, packet)
		self.nextTime = packet.Timestamp
		self.nextSeqNum = nextSeq
		nextSeq++
	}
	return
}

type RTPDepacketizer struct {
	frame         []byte
	timestamp     uint32
	h264Unmarshal *codecs.H264Packet
}

func NewDepacketizer() *RTPDepacketizer {
	return &RTPDepacketizer{
		frame:         make([]byte, 0),
		h264Unmarshal: &codecs.H264Packet{},
	}
}

func (self *RTPDepacketizer) AddPacket(pkt *rtp.Packet) ([]byte, uint32) {

	ts := pkt.Timestamp

	if self.timestamp != ts {
		self.frame = make([]byte, 0)
	}

	self.timestamp = ts

	buf, _ := self.h264Unmarshal.Unmarshal(pkt.Payload)

	self.frame = append(self.frame, buf...)

	if !pkt.Marker {
		return nil, 0
	}

	return self.frame, self.timestamp
}

func test(c *gin.Context) {
	c.String(200, "Hello World")
}

func index(c *gin.Context) {
	c.HTML(200, "index.html", gin.H{})
}

type Recorder struct {
	flvfile       *os.File
	h264file      *os.File
	muxer         *flv.Muxer
	audiojitter   *RTPJitter
	videojitter   *RTPJitter
	h264Unmarshal *codecs.H264Packet
	depacketizer  *RTPDepacketizer
}

func newRecorder(filename string) *Recorder {
	file, err := os.Create(filename)
	h264file, err := os.Create("test.h264")
	if err != nil {
		panic(err)
	}
	return &Recorder{
		flvfile:       file,
		h264file:      h264file,
		muxer:         flv.NewMuxer(file),
		audiojitter:   NewJitter(512, 48000),
		videojitter:   NewJitter(512, 90000),
		h264Unmarshal: &codecs.H264Packet{},
		depacketizer:  NewDepacketizer(),
	}
}

func (r *Recorder) PushAudio(pkt *rtp.Packet) {

}

func (r *Recorder) PushVideo(pkt *rtp.Packet) {

	r.videojitter.Add(pkt)
	pkts := r.videojitter.GetOrdered()

	//if pkts != nil {
	//	for _, _pkt := range pkts {
	//		frame, _ := r.depacketizer.AddPacket(_pkt)
	//		if frame != nil {
	//			fmt.Println("Write frame ", len(frame))
	//			r.h264file.Write(frame)
	//		}
	//	}
	//}

	if pkts != nil {
		for _, _pkt := range pkts {
			fmt.Println("seq", _pkt.SequenceNumber)
			buf, err := r.h264Unmarshal.Unmarshal(_pkt.Payload)
			if err != nil {
				fmt.Println(err)
			}
			r.h264file.Write(buf)
		}
	}
}

func (r *Recorder) Close() {
	if r.flvfile != nil {
		r.flvfile.Close()
	}
	if r.h264file != nil {
		r.h264file.Close()
	}
}

func publishStream(c *gin.Context) {

	var data struct {
		Sdp string `json:"sdp"`
	}

	if err := c.ShouldBind(&data); err != nil {
		c.JSON(200, gin.H{"s": 10001, "e": err})
		return
	}

	var config = webrtc.Configuration{
		ICEServers:   []webrtc.ICEServer{},
		BundlePolicy: webrtc.BundlePolicyMaxBundle,
		SDPSemantics: webrtc.SDPSemanticsUnifiedPlan,
	}

	var media = webrtc.MediaEngine{}
	media.RegisterCodec(webrtc.NewRTPOpusCodec(webrtc.DefaultPayloadTypeOpus, 48000))
	media.RegisterCodec(webrtc.NewRTPH264Codec(webrtc.DefaultPayloadTypeH264, 90000))

	api := webrtc.NewAPI(webrtc.WithMediaEngine(media))

	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  data.Sdp,
	}
	err = peerConnection.SetRemoteDescription(offer)
	if err != nil {
		panic(err)
	}
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		fmt.Println(err)
		panic(err)
	}
	peerConnection.SetLocalDescription(answer)

	recorder := newRecorder("record.flv")

	peerConnection.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {

		fmt.Printf("Track has started, of type %d: %s \n", track.PayloadType(), track.Codec().Name)

		for {
			rtp, readErr := track.ReadRTP()
			if readErr != nil {
				if readErr == io.EOF {
					return
				}
				panic(readErr)
			}
			switch track.Kind() {
			case webrtc.RTPCodecTypeAudio:
				recorder.PushAudio(rtp)
			case webrtc.RTPCodecTypeVideo:
				recorder.PushVideo(rtp)
			}
		}
	})

	c.JSON(200, gin.H{
		"s": 10000,
		"d": map[string]string{
			"sdp": answer.SDP,
		},
	})
}


func main() {

	router := gin.Default()
	corsc := cors.DefaultConfig()
	corsc.AllowAllOrigins = true
	corsc.AllowCredentials = true
	router.Use(cors.New(corsc))

	router.LoadHTMLFiles("./static/index.html")

	router.GET("/test", test)

	router.GET("/", index)

	router.POST("/rtc/v1/publish", publishStream)

	router.Run(":8080")
}
