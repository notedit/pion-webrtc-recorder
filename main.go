package main

import (
	"fmt"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/notedit/rtmp/format/flv"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v2"
	"github.com/pion/webrtc/v2/pkg/media/samplebuilder"
	"io"
	"os"
)

func test(c *gin.Context) {
	c.String(200, "Hello World")
}

func index(c *gin.Context) {
	c.HTML(200, "index.html", gin.H{})
}

type Recorder struct {
	file  *os.File
	muxer *flv.Muxer
	audioBuilder *samplebuilder.SampleBuilder
	videoBuilder *samplebuilder.SampleBuilder
}

func newRecorder(filename string) *Recorder {
	file,err := os.Create(filename)
	if err != nil {
		panic(err)
	}
	return &Recorder{
		file:         file,
		muxer:        flv.NewMuxer(file),
		audioBuilder: samplebuilder.New(20, &codecs.OpusPacket{}),
		videoBuilder: samplebuilder.New(20,&codecs.H264Packet{}),
	}
}

func (r *Recorder) PushAudio(pkt *rtp.Packet) {

	r.audioBuilder.Push(pkt)

	for {
		sample := r.audioBuilder.Pop()
		if sample == nil {
			return
		}
		fmt.Println("audio sample ", sample.Samples)
	}
}

func (r *Recorder) PushVideo(pkt *rtp.Packet) {

	r.videoBuilder.Push(pkt)

	for {
		sample := r.videoBuilder.Pop()
		if sample == nil {
			return
		}
		fmt.Println("video sample ", sample.Samples)
	}
}

func (r *Recorder) Close() {
	if r.file != nil {
		r.file.Close()
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
			rtp,readErr := track.ReadRTP()
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
