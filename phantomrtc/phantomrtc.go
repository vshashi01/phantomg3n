package phantomrtc

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

// PhantomPeerManager The thread safe connection manager for all relevant PeerConnections
type PhantomPeerManager struct {
	mutex           sync.RWMutex
	peerConnections []*phantomPeerConnection
}

type phantomPeerMessage struct {
	Event string `json:"event"`
	Data  string `json:"data"`
}

// PhantomPeerInterface Interface to the internal phantomPeer structs.
type PhantomPeerInterface interface {
	Close()
	RunWebsocket()
	IsActive() bool
}

type phantomPeerConnection struct {
	mutex          sync.RWMutex
	peerConnection *webrtc.PeerConnection
	websocket      *websocket.Conn
}

func newPhantomPeer(peerConnection *webrtc.PeerConnection, websocket *websocket.Conn) *phantomPeerConnection {
	phantomPeer := new(phantomPeerConnection)
	phantomPeer.peerConnection = peerConnection
	phantomPeer.websocket = websocket
	phantomPeer.mutex = sync.RWMutex{}

	return phantomPeer
}

// Close closes the speciic PhantomPeer
func (phantomPeer *phantomPeerConnection) Close() {
	phantomPeer.mutex.Lock()
	defer phantomPeer.mutex.Unlock()

	phantomPeer.peerConnection.Close()
	phantomPeer.websocket.WriteJSON(&phantomPeerMessage{Event: "close",
		Data: "connection closed"})
	phantomPeer.websocket.Close()

	phantomPeer.peerConnection = nil
	phantomPeer.websocket = nil

	return
}

// Runs the infinite loop to read from websocket
func (phantomPeer *phantomPeerConnection) RunWebsocket() {
	message := &phantomPeerMessage{}
	for {

		// return if the connection is already closed
		if !phantomPeer.IsActive() {
			return
		}

		fmt.Println("Reading message")
		_, raw, err := phantomPeer.websocket.ReadMessage()

		if err != nil {
			fmt.Println(err)
			return
		} else if err := json.Unmarshal(raw, &message); err != nil {
			fmt.Println(err)
			return
		}

		fmt.Println("Switch message")

		switch message.Event {
		case "candidate":
			fmt.Println("Got Candidate")
			candidate := webrtc.ICECandidateInit{}
			if err = json.Unmarshal([]byte(message.Data), &candidate); err != nil {
				log.Println(err)
				return
			}

			if err = phantomPeer.peerConnection.AddICECandidate(candidate); err != nil {
				log.Println(err)
				return
			}
		case "answer":
			fmt.Println("Got Answer")
			answer := webrtc.SessionDescription{}
			if err = json.Unmarshal([]byte(message.Data), &answer); err != nil {
				log.Println(err)
				return
			}

			if err = phantomPeer.peerConnection.SetRemoteDescription(answer); err != nil {
				log.Println(err)
				return
			}
		}
	}
}

// IsActive checks if the peer connection is valid
func (phantomPeer *phantomPeerConnection) IsActive() bool {
	phantomPeer.mutex.RLock()
	defer phantomPeer.mutex.RUnlock()

	if (phantomPeer.peerConnection == nil) || (phantomPeer.peerConnection.ConnectionState() == webrtc.PeerConnectionStateClosed) {
		//ignore disconnected connections
		return false
	}

	return true
}

// Close closes the speciic PhantomPeer
func (phantomPeer *phantomPeerConnection) setupTrack(track *webrtc.TrackLocalStaticRTP) error {
	phantomPeer.mutex.Lock()
	defer phantomPeer.mutex.Unlock()

	fmt.Println("Setup track called")

	trackIsAlreadyAdded := false

	for _, receiver := range phantomPeer.peerConnection.GetReceivers() {
		if receiver.Track() != nil {
			trackIsAlreadyAdded = true
		}
	}

	if trackIsAlreadyAdded {
		return errors.New("Track already added")
	}

	if track == nil {
		return errors.New("Render Track is nil")
	}

	rtpSender, err := phantomPeer.peerConnection.AddTrack(track)
	if err != nil {
		return err
	}
	fmt.Println("Track created")

	// Read incoming RTCP packets
	// Before these packets are retuned they are processed by interceptors. For things
	// like NACK this needs to be called.
	go func() {
		rtcpBuf := make([]byte, 1500)
		for {
			if _, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {
				fmt.Println("RTPSender ended")
				return
			}
		}
	}()

	fmt.Println("RTPSender created")

	offer, err := phantomPeer.peerConnection.CreateOffer(nil)
	if err != nil {
		return err
	}

	fmt.Println("Offer created")

	err = phantomPeer.peerConnection.SetLocalDescription(offer)
	if err != nil {
		return err
	}

	offerString, err := json.Marshal(offer)
	if err != nil {
		return err
	}

	fmt.Println("Offer string encoded.")

	if err = phantomPeer.websocket.WriteJSON(&phantomPeerMessage{
		Event: "offer",
		Data:  string(offerString),
	}); err != nil {
		return err
	}

	fmt.Println("Offer is sent")

	return nil
}

func (phantomPeer *phantomPeerConnection) dispatchKeyFrame() {
	phantomPeer.mutex.Lock()
	defer phantomPeer.mutex.Unlock()

	for _, receiver := range phantomPeer.peerConnection.GetReceivers() {
		if receiver.Track() == nil {
			continue
		}

		_ = phantomPeer.peerConnection.WriteRTCP([]rtcp.Packet{
			&rtcp.PictureLossIndication{
				MediaSSRC: uint32(receiver.Track().SSRC()),
			},
		})
	}
}

// NewPhantomPeerManager with all variables initialized
func NewPhantomPeerManager() *PhantomPeerManager {
	phantomManager := new(PhantomPeerManager)
	phantomManager.mutex = sync.RWMutex{}
	//phantomManager.peerConnections = make([]*phantomPeerConnection, 1)

	return phantomManager
}

// Close Closes the PantomPeer at the index. Need to call Remove manually to ensure the connection is removed from the manager list.
func (manager *PhantomPeerManager) Close(index int) error {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()

	if len(manager.peerConnections) > index {
		return errors.New("index out of range")
	}

	manager.peerConnections[index].Close()

	return nil
}

// Remove the connecton Container at index from the list
func (manager *PhantomPeerManager) Remove(index int) error {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()

	if len(manager.peerConnections) > index {
		return errors.New("index out of range")
	}

	manager.peerConnections = append(manager.peerConnections[:index], manager.peerConnections[index+1:]...)

	return nil
}

// CloseAll closes al the connection sequentially.
func (manager *PhantomPeerManager) CloseAll() {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()

	for _, phantomPeer := range manager.peerConnections {
		phantomPeer.Close()
	}
}

// RemoveAllClosed removes all the closed connections from the list
func (manager *PhantomPeerManager) RemoveAllClosed() {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()

	index := 0

	//check for connection state and move the valid connections to the front of the slice
	for _, phantomPeer := range manager.peerConnections {

		if phantomPeer.IsActive() {
			manager.peerConnections[index] = phantomPeer
			index++
		}
	}

	// Prevent memory leak by erasing truncated values
	// (not needed if values don't cotain pointers, directly or indirectly)
	for j := index; j < len(manager.peerConnections); j++ {
		manager.peerConnections[j] = nil

		manager.peerConnections = manager.peerConnections[:index]
	}
}

// SignalPeerConnections updates each PeerConnection so that it is getting all the expected mdia tracks
// Only reads the connction controls.
func (manager *PhantomPeerManager) SignalPeerConnections(track *webrtc.TrackLocalStaticRTP) {
	manager.mutex.RLock()
	defer manager.mutex.RUnlock()

	for _, phantomPeer := range manager.peerConnections {
		if !phantomPeer.IsActive() {
			//ignore disconnected connections
			continue
		}
		err := phantomPeer.setupTrack(track)
		if err != nil {
			fmt.Println(err)
		}
	}
}

// DispatchKeyFrameToAllPeer sends a keyframe to all PeerConnections, should be called after all SignalPeerConnection
func (manager *PhantomPeerManager) DispatchKeyFrameToAllPeer() {
	manager.mutex.RLock()
	defer manager.mutex.RUnlock()

	for _, phantomPeer := range manager.peerConnections {
		phantomPeer.dispatchKeyFrame()
	}
}

// CreateNewConnection Create a new connection and adds to target manager.
func (manager *PhantomPeerManager) CreateNewConnection(websocket *websocket.Conn) (PhantomPeerInterface, error) {

	// Prepare WebRTC configuration
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{
					// "stun:stun.l.google.com:19302",
					// "stun:stun1.l.google.com:19302",
					// "stun:stun2.l.google.com:19302",
					// "stun:stun3.l.google.com:19302",
					// "stun:stun4.l.google.com:19302"
					"turn:18.220.247.163:3478",
				},
				Username:       "user",
				Credential:     "root",
				CredentialType: webrtc.ICECredentialTypePassword,
			},
		},
	}

	// Create new PeerConnection
	// Setup all neessary state callbacks
	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		log.Print(err)
		return nil, err
	}

	// Trickle ICE Emit server candidate to client
	peerConnection.OnICECandidate(func(i *webrtc.ICECandidate) {
		if i == nil {
			return
		}

		candidateString, err := json.Marshal(i.ToJSON())
		if err == nil {
			log.Println(err)
			return
		}

		writeErr := websocket.WriteJSON(&phantomPeerMessage{
			Event: "candidate",
			Data:  string(candidateString)})
		if writeErr != nil {
			log.Println(writeErr)
		}
		fmt.Println("Sent a candidate")
	})

	// If PeerCnnection is closed remove it from global list
	peerConnection.OnConnectionStateChange(func(p webrtc.PeerConnectionState) {
		fmt.Println("Peer Connection State ", p.String())
		switch p {
		case webrtc.PeerConnectionStateFailed:
			if err := peerConnection.Close(); err != nil {
				log.Print(err)
			}
		}
	})

	peerConnection.OnICEConnectionStateChange(func(i webrtc.ICEConnectionState) {
		fmt.Println("Ice Connection State ", i.String())
	})

	phantomPeer := newPhantomPeer(peerConnection, websocket)

	// Add our new PeerConnection to global list
	manager.mutex.Lock()
	manager.peerConnections = append(manager.peerConnections, phantomPeer)
	manager.mutex.Unlock()

	return phantomPeer, nil
}
