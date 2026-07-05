package homekit

import (
	"errors"
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/pkg/aac"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/h264"
	"github.com/AlexxIT/go2rtc/pkg/hap/camera"
	"github.com/AlexxIT/go2rtc/pkg/hap/hds"
	"github.com/AlexxIT/go2rtc/pkg/hap/tlv8"
	"github.com/AlexxIT/go2rtc/pkg/iso"
	"github.com/AlexxIT/go2rtc/pkg/mp4"
	"github.com/pion/rtp"
)

const (
	// recKeepDuration - prebuffer ring size (wall time), should cover
	// the selected prebuffer (4s) + fragment (4s) + processing margin
	recKeepDuration = 12 * time.Second

	// recMaxDuration - failsafe cap for a single recording session,
	// normally the hub closes the session itself when motion ends
	recMaxDuration = 3 * time.Minute

	// recChunkSize - max size of a single dataSend chunk (256 KiB),
	// HDS frames are limited to 1 MB
	recChunkSize = 0x40000

	// recFrameTimeout - fail the session if the producer stops
	// delivering frames
	recFrameTimeout = 15 * time.Second
)

const (
	recTrackVideo = 0
	recTrackAudio = 1
)

type recFrame struct {
	track   byte
	key     bool
	ts      uint32 // RTP timestamp (in codec clockrate)
	cts     uint32
	wall    time.Time
	payload []byte
}

// recConsumer - stream consumer with rolling prebuffer. Attached to the
// stream while HKSV recording is enabled, so the camera producer runs
// continuously and go2rtc always has the last seconds of video.
type recConsumer struct {
	core.Connection

	mu       sync.Mutex
	buf      []recFrame
	bytes    int
	sub      chan recFrame
	overflow bool

	videoCodec *core.Codec
	audioCodec *core.Codec
}

func newRecConsumer() *recConsumer {
	return &recConsumer{
		Connection: core.Connection{
			ID:         core.NewID(),
			FormatName: "homekit/hksv",
			Medias: []*core.Media{
				{
					Kind:      core.KindVideo,
					Direction: core.DirectionSendonly,
					Codecs: []*core.Codec{
						{Name: core.CodecH264},
					},
				},
				{
					Kind:      core.KindAudio,
					Direction: core.DirectionSendonly,
					Codecs: []*core.Codec{
						{Name: core.CodecAAC},
					},
				},
			},
		},
	}
}

func (c *recConsumer) AddTrack(media *core.Media, _ *core.Codec, track *core.Receiver) error {
	codec := track.Codec.Clone()
	handler := core.NewSender(media, codec)

	switch track.Codec.Name {
	case core.CodecH264:
		c.videoCodec = codec
		handler.Handler = func(packet *rtp.Packet) {
			c.push(recFrame{
				track: recTrackVideo,
				key:   h264.IsKeyframe(packet.Payload),
				ts:    packet.Timestamp,
				cts:   uint32(packet.ExtensionProfile), // same hack as mp4.Muxer
				wall:  time.Now(),
				// important to copy payload, depayloaders reuse the buffer,
				// but the recorder muxes the frame only seconds later
				payload: append([]byte(nil), packet.Payload...),
			})
		}

		if track.Codec.IsRTP() {
			handler.Handler = h264.RTPDepay(track.Codec, handler.Handler)
		} else {
			handler.Handler = h264.RepairAVCC(track.Codec, handler.Handler)
		}

	case core.CodecAAC:
		c.audioCodec = codec
		handler.Handler = func(packet *rtp.Packet) {
			c.push(recFrame{
				track:   recTrackAudio,
				ts:      packet.Timestamp,
				wall:    time.Now(),
				payload: append([]byte(nil), packet.Payload...),
			})
		}

		if track.Codec.IsRTP() {
			handler.Handler = aac.RTPDepay(handler.Handler)
		}

	default:
		return errors.New("homekit: unsupported codec: " + track.Codec.String())
	}

	handler.HandleRTP(track)
	c.Senders = append(c.Senders, handler)
	return nil
}

func (c *recConsumer) push(frame recFrame) {
	c.mu.Lock()

	c.buf = append(c.buf, frame)
	c.bytes += len(frame.payload)
	c.prune()

	if c.sub != nil {
		select {
		case c.sub <- frame:
		default:
			c.overflow = true
		}
	}

	c.mu.Unlock()
}

// hard limits for the prebuffer, protection from streams with rare
// keyframes (GOP > recKeepDuration) or without keyframes at all
const (
	recMaxFrames = 2048     // ~1 minute of 30 fps video with audio
	recMaxBytes  = 32 << 20 // 32 MB
)

// prune drops whole GOPs from the head of the buffer. Every buffer state
// starts with a video keyframe, so a recording can start on any state.
func (c *recConsumer) prune() {
	cut := time.Now().Add(-recKeepDuration)

	for {
		// find second video keyframe
		i := c.secondKeyframe()
		if i < 0 || c.buf[i].wall.After(cut) {
			break
		}
		// safe to drop first GOP (and any audio before this keyframe)
		c.drop(i)
	}

	// keyframe-aligned pruning is not enough for streams with rare
	// keyframes, so force hard limits (buffer may start mid GOP)
	for len(c.buf) > recMaxFrames || c.bytes > recMaxBytes {
		c.drop(max(len(c.buf)/4, 1))
	}
}

func (c *recConsumer) drop(n int) {
	for i := 0; i < n; i++ {
		c.bytes -= len(c.buf[i].payload)
	}
	c.buf = c.buf[n:]
}

func (c *recConsumer) secondKeyframe() int {
	n := 0
	for i := range c.buf {
		if c.buf[i].track == recTrackVideo && c.buf[i].key {
			if n++; n == 2 {
				return i
			}
		}
	}
	return -1
}

// subscribe returns buffered frames starting from the keyframe closest to
// (now - prebuffer) and a channel with all subsequent frames.
func (c *recConsumer) subscribe(prebuffer time.Duration) ([]recFrame, chan recFrame, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.sub != nil {
		return nil, nil, errors.New("homekit: recording already in progress")
	}

	target := time.Now().Add(-prebuffer)

	start := -1
	for i := range c.buf {
		if c.buf[i].track != recTrackVideo || !c.buf[i].key {
			continue
		}
		if start < 0 || c.buf[i].wall.Before(target) || c.buf[i].wall.Equal(target) {
			start = i
		}
		if !c.buf[i].wall.Before(target) {
			break
		}
	}

	if start < 0 {
		return nil, nil, errors.New("homekit: no keyframe in prebuffer")
	}

	frames := make([]recFrame, len(c.buf)-start)
	copy(frames, c.buf[start:])

	c.overflow = false
	c.sub = make(chan recFrame, 512)

	return frames, c.sub, nil
}

func (c *recConsumer) unsubscribe() {
	c.mu.Lock()
	c.sub = nil
	c.overflow = false
	c.mu.Unlock()
}

func (c *recConsumer) hasOverflow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.overflow
}

// recSession - active HKSV recording (one dataSend stream).
type recSession struct {
	conn     *hds.Conn
	streamID int64

	mu     sync.Mutex
	closed bool
}

func (s *recSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// close marks the session as closed. With a reason >= 0 it also notifies
// the controller with a dataSend close event.
func (s *recSession) close(reason int64) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	if reason >= 0 {
		_ = s.conn.WriteEvent(hds.ProtoDataSend, hds.TopicClose, map[string]any{
			"streamId": s.streamID,
			"reason":   reason,
		})
	}
}

// sendChunks splits data into chunks and sends them as dataSend data events.
func (s *recSession) sendChunks(data []byte, seq int64, dataType string, endOfStream bool) error {
	chunkSeq := int64(1)

	for offset := 0; offset < len(data); offset += recChunkSize {
		if s.isClosed() {
			return errors.New("homekit: recording session closed")
		}

		end := offset + recChunkSize
		if end > len(data) {
			end = len(data)
		}
		last := end >= len(data)

		metadata := map[string]any{
			"dataType":                dataType,
			"dataSequenceNumber":      seq,
			"dataChunkSequenceNumber": chunkSeq,
			"isLastDataChunk":         last,
		}
		if chunkSeq == 1 {
			metadata["dataTotalSize"] = int64(len(data))
		}

		message := map[string]any{
			"streamId": s.streamID,
			"packets": []any{
				map[string]any{
					"data":     data[offset:end],
					"metadata": metadata,
				},
			},
		}
		if last {
			message["endOfStream"] = endOfStream
		}

		_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := s.conn.WriteEvent(hds.ProtoDataSend, hds.TopicData, message); err != nil {
			return err
		}

		chunkSeq++
	}

	return nil
}

// fragmenter assembles keyframe-aligned fMP4 fragments from raw frames.
type fragmenter struct {
	videoCodec *core.Codec
	audioCodec *core.Codec // nil - video only recording

	fragmentDur uint32 // in video clockrate units

	seq    uint32
	vidDTS uint64
	audDTS uint64

	vidSamples []mp4.FragmentSample
	audSamples []mp4.FragmentSample
	vidDur     uint32 // duration of vidSamples

	prevVid *recFrame
	prevAud *recFrame
}

func newFragmenter(videoCodec, audioCodec *core.Codec, fragmentLength uint32) *fragmenter {
	if fragmentLength == 0 {
		fragmentLength = 4000
	}
	return &fragmenter{
		videoCodec:  videoCodec,
		audioCodec:  audioCodec,
		fragmentDur: uint32(uint64(fragmentLength) * uint64(videoCodec.ClockRate) / 1000),
	}
}

// init returns the fMP4 initialization segment (ftyp + moov).
func (f *fragmenter) init() ([]byte, error) {
	muxer := &mp4.Muxer{}
	muxer.AddTrack(f.videoCodec)
	if f.audioCodec != nil {
		muxer.AddTrack(f.audioCodec)
	}
	return muxer.GetInit()
}

// push consumes the next frame. Returns a complete fragment (moof + mdat)
// when the frame is a video keyframe and the current fragment reached the
// target duration, otherwise nil.
func (f *fragmenter) push(frame recFrame) []byte {
	switch frame.track {
	case recTrackVideo:
		var fragment []byte

		if f.prevVid != nil {
			// wrap-around safe, clamp only pathological gaps (>10s),
			// real gaps should keep the timeline correct
			duration := frame.ts - f.prevVid.ts
			if duration == 0 || duration > 10*f.videoCodec.ClockRate {
				duration = f.videoCodec.ClockRate/30 + 1
			}
			f.append(f.prevVid, duration)

			if frame.key && f.vidDur >= f.fragmentDur {
				fragment = f.marshal()
			}
		}

		clone := frame
		f.prevVid = &clone

		return fragment

	case recTrackAudio:
		if f.audioCodec == nil {
			return nil
		}
		if f.prevAud != nil {
			duration := frame.ts - f.prevAud.ts
			if duration == 0 || duration > 10*f.audioCodec.ClockRate {
				duration = 1024 // default AAC frame size
			}
			f.append(f.prevAud, duration)
		}

		clone := frame
		f.prevAud = &clone
	}

	return nil
}

func (f *fragmenter) append(frame *recFrame, duration uint32) {
	switch frame.track {
	case recTrackVideo:
		var flags uint32
		if frame.key {
			flags = iso.SampleVideoIFrame
		} else {
			flags = iso.SampleVideoNonIFrame
		}
		f.vidSamples = append(f.vidSamples, mp4.FragmentSample{
			Duration: duration, Flags: flags, CTS: frame.cts, Data: frame.payload,
		})
		f.vidDur += duration

	case recTrackAudio:
		f.audSamples = append(f.audSamples, mp4.FragmentSample{
			Duration: duration, Flags: iso.SampleAudio, Data: frame.payload,
		})
	}
}

// marshal builds the current fragment and resets the state.
func (f *fragmenter) marshal() []byte {
	if len(f.vidSamples) == 0 {
		return nil
	}

	f.seq++

	tracks := []mp4.FragmentTrack{
		{ID: 1, DTS: f.vidDTS, Samples: f.vidSamples},
	}
	for i := range f.vidSamples {
		f.vidDTS += uint64(f.vidSamples[i].Duration)
	}

	if len(f.audSamples) > 0 {
		tracks = append(tracks, mp4.FragmentTrack{ID: 2, DTS: f.audDTS, Samples: f.audSamples})
		for i := range f.audSamples {
			f.audDTS += uint64(f.audSamples[i].Duration)
		}
	}

	fragment := mp4.MarshalFragment(f.seq, tracks)

	f.vidSamples = nil
	f.audSamples = nil
	f.vidDur = 0

	return fragment
}

// serveHDS reads messages from an established HDS connection and dispatches
// the dataSend protocol (HKSV recordings).
func (s *server) serveHDS(conn *hds.Conn) {
	s.AddConn(conn)

	defer func() {
		s.closeRecording(conn, -1)
		s.DelConn(conn)
		_ = conn.Close()
	}()

	for {
		msg, err := conn.ReadMessage()
		if err != nil {
			return
		}

		log.Trace().Str("stream", s.stream).Msgf("[homekit] hds %s", msg)

		if msg.Protocol != hds.ProtoDataSend {
			if msg.Type == hds.TypeRequest {
				_ = conn.WriteResponse(msg.Protocol, msg.Topic, msg.ID, hds.StatusMissingProtocol, nil)
			}
			continue
		}

		switch {
		case msg.Type == hds.TypeRequest && msg.Topic == hds.TopicOpen:
			s.handleDataSendOpen(conn, msg)

		case msg.Type == hds.TypeEvent && msg.Topic == hds.TopicClose:
			reason, _ := msg.Message["reason"].(int64)
			log.Debug().Str("stream", s.stream).Msgf("[homekit] hksv close recording, reason=%d", reason)
			s.closeRecording(conn, -1)

		case msg.Type == hds.TypeEvent && msg.Topic == hds.TopicAck:
			log.Debug().Str("stream", s.stream).Msg("[homekit] hksv recording ack")
			s.closeRecording(conn, -1)
		}
	}
}

func (s *server) handleDataSendOpen(conn *hds.Conn, msg *hds.Message) {
	streamID, _ := msg.Message["streamId"].(int64)
	target, _ := msg.Message["target"].(string)
	streamType, _ := msg.Message["type"].(string)

	reject := func(reason int64) {
		_ = conn.WriteResponse(hds.ProtoDataSend, hds.TopicOpen, msg.ID, hds.StatusProtocolSpecificError, map[string]any{
			"status": reason,
		})
	}

	if target != "controller" || streamType != "ipcamera.recording" {
		log.Warn().Str("stream", s.stream).Msgf("[homekit] wrong dataSend open: target=%s type=%s", target, streamType)
		reject(hds.ReasonUnsupported)
		return
	}

	s.recMu.Lock()

	switch {
	case !s.recordingActive || !s.cameraActive:
		s.recMu.Unlock()
		reject(hds.ReasonNotAllowed)
		return
	case s.recSelected == nil, s.recConsumer == nil:
		s.recMu.Unlock()
		reject(hds.ReasonInvalidConfiguration)
		return
	case s.recSession != nil:
		s.recMu.Unlock()
		reject(hds.ReasonBusy)
		return
	}

	session := &recSession{conn: conn, streamID: streamID}
	s.recSession = session

	consumer := s.recConsumer
	selected := *s.recSelected
	audio := s.audioActive

	s.recMu.Unlock()

	if err := conn.WriteResponse(hds.ProtoDataSend, hds.TopicOpen, msg.ID, hds.StatusSuccess, map[string]any{
		"status": int64(hds.StatusSuccess),
	}); err != nil {
		s.closeRecording(conn, -1)
		return
	}

	log.Debug().Str("stream", s.stream).Msgf("[homekit] hksv start recording, id=%d", streamID)

	go s.recordTo(session, consumer, &selected, audio)
}

// closeRecording stops the active recording session (if it belongs to conn).
func (s *server) closeRecording(conn *hds.Conn, reason int64) {
	s.recMu.Lock()
	session := s.recSession
	if session != nil && (conn == nil || session.conn == conn) {
		s.recSession = nil
	} else {
		session = nil
	}
	s.recMu.Unlock()

	if session != nil {
		session.close(reason)
	}
}

// recordTo pumps fMP4 fragments to the controller: init segment, prebuffer
// and live video until the controller closes the stream.
func (s *server) recordTo(session *recSession, consumer *recConsumer, selected *camera.SelectedCameraRecordingConfiguration, audio bool) {
	defer func() {
		s.recMu.Lock()
		if s.recSession == session {
			s.recSession = nil
		}
		s.recMu.Unlock()
	}()

	prebuffer := time.Duration(selected.GeneralConfig.PrebufferLength) * time.Millisecond
	if prebuffer <= 0 {
		prebuffer = 4 * time.Second
	}

	frames, live, err := consumer.subscribe(prebuffer)
	if err != nil {
		log.Warn().Err(err).Str("stream", s.stream).Msg("[homekit] hksv recording")
		session.close(hds.ReasonUnexpectedFailure)
		return
	}
	defer consumer.unsubscribe()

	audioCodec := consumer.audioCodec
	if !audio {
		audioCodec = nil
	}

	frag := newFragmenter(consumer.videoCodec, audioCodec, selected.GeneralConfig.MediaContainerConfig.MediaContainerParams.FragmentLength)

	init, err := frag.init()
	if err != nil {
		log.Warn().Err(err).Str("stream", s.stream).Msg("[homekit] hksv recording")
		session.close(hds.ReasonUnexpectedFailure)
		return
	}

	if err = session.sendChunks(init, 1, "mediaInitialization", false); err != nil {
		session.close(-1)
		return
	}

	seq := int64(2) // 1 - init segment, 2+ - media fragments
	deadline := time.Now().Add(recMaxDuration)
	timeout := time.NewTimer(recFrameTimeout)
	defer timeout.Stop()

	push := func(frame recFrame) bool {
		fragment := frag.push(frame)
		if fragment == nil {
			return true
		}

		endOfStream := time.Now().After(deadline)
		if err := session.sendChunks(fragment, seq, "mediaFragment", endOfStream); err != nil {
			session.close(-1)
			return false
		}
		seq++

		if endOfStream {
			log.Debug().Str("stream", s.stream).Msg("[homekit] hksv max recording duration")
			return false
		}
		return true
	}

	for _, frame := range frames {
		if session.isClosed() {
			return
		}
		if !push(frame) {
			return
		}
	}

	for {
		if session.isClosed() {
			return
		}
		if consumer.hasOverflow() {
			log.Warn().Str("stream", s.stream).Msg("[homekit] hksv recording overflow")
			session.close(hds.ReasonUnexpectedFailure)
			return
		}

		select {
		case frame := <-live:
			// only video resets the timeout: a session with audio but
			// without video frames can't produce fragments and should die
			// (also protects the fragmenter audio buffer from growing)
			if frame.track == recTrackVideo {
				if !timeout.Stop() {
					select {
					case <-timeout.C:
					default:
					}
				}
				timeout.Reset(recFrameTimeout)
			}

			if !push(frame) {
				return
			}
		case <-timeout.C:
			log.Warn().Str("stream", s.stream).Msg("[homekit] hksv recording timeout")
			session.close(hds.ReasonTimeout)
			return
		}
	}
}

// enableSecureVideo adds HKSV services to the accessory and restores the
// recording state from the config. Should be called on server creation.
func (s *server) enableSecureVideo(recordingConfig string, recordingActive, cameraActive, audioActive bool) error {
	services, err := camera.SecureVideoServices(camera.RecordingOptions{})
	if err != nil {
		return err
	}

	s.accessory.Services = append(s.accessory.Services, services...)
	s.accessory.InitIID()

	rm := s.accessory.GetService(camera.TypeServiceCameraRecordingManagement)
	om := s.accessory.GetService(camera.TypeServiceCameraOperatingMode)
	ds := s.accessory.GetService(camera.TypeServiceDataStreamTransportManagement)

	// link DataStreamTransportManagement to CameraRecordingManagement
	rm.Linked = []int{int(ds.IID)}

	s.secureVideo = true
	s.recordingActive = recordingActive
	s.cameraActive = cameraActive
	s.audioActive = audioActive

	// restore characteristic values
	rm.GetCharacter(camera.TypeActive).Value = b2i(recordingActive)
	rm.GetCharacter(camera.TypeRecordingAudioActive).Value = b2i(audioActive)
	om.GetCharacter(camera.TypeHomeKitCameraActive).Value = b2i(cameraActive)
	s.accessory.GetCharacter(camera.TypeStatusActive).Value = cameraActive

	if recordingConfig != "" {
		var conf camera.SelectedCameraRecordingConfiguration
		if err = tlv8.UnmarshalBase64(recordingConfig, &conf); err != nil {
			log.Warn().Err(err).Msgf("[homekit] wrong recording_config for %s", s.stream)
		} else {
			rm.GetCharacter(camera.TypeSelectedCameraRecordingConfiguration).Value = recordingConfig
			s.recSelected = &conf
		}
	}

	s.armRecorder()

	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// armRecorder attaches or detaches the prebuffer consumer based on the
// current recording state. Should be called after every state change.
//
// Consumer start/stop and session close are blocking calls (camera dial,
// network writes), so they run outside recMu. Transitions are serialized
// with armMu, and the state is re-checked on every call, so concurrent
// state changes converge to the right consumer state.
func (s *server) armRecorder() {
	s.armMu.Lock()
	defer s.armMu.Unlock()

	s.recMu.Lock()
	armed := s.secureVideo && s.recordingActive && s.cameraActive && s.recSelected != nil
	attached := s.recConsumer != nil
	s.recMu.Unlock()

	if armed == attached {
		return
	}

	stream := streams.Get(s.stream)
	if stream == nil {
		return
	}

	if armed {
		consumer := newRecConsumer()
		if err := stream.AddConsumer(consumer); err != nil {
			log.Error().Err(err).Str("stream", s.stream).Msg("[homekit] hksv can't start prebuffer")
			return
		}
		if consumer.videoCodec == nil {
			log.Error().Str("stream", s.stream).Msg("[homekit] hksv requires H264 video")
			stream.RemoveConsumer(consumer)
			return
		}

		s.recMu.Lock()
		s.recConsumer = consumer
		s.recMu.Unlock()

		log.Debug().Str("stream", s.stream).Msg("[homekit] hksv prebuffer started")
	} else {
		s.recMu.Lock()
		consumer := s.recConsumer
		session := s.recSession
		s.recConsumer = nil
		s.recSession = nil
		s.recMu.Unlock()

		if session != nil {
			session.close(hds.ReasonNotAllowed)
		}
		if consumer != nil {
			stream.RemoveConsumer(consumer)
		}

		log.Debug().Str("stream", s.stream).Msg("[homekit] hksv prebuffer stopped")
	}
}
