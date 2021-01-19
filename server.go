package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	uuid "github.com/satori/go.uuid"
	"github.com/vshashi01/webg3n/phantomrtc"
	"github.com/vshashi01/webg3n/renderer"
)

const (
	writeTimeout   = 10 * time.Second
	readTimeout    = 60 * time.Second
	pingPeriod     = (readTimeout * 9) / 10
	maxMessageSize = 512
)

// Client holding g3napp, socket and channels
type Client struct {
	app                   renderer.RenderingApp
	conn                  *websocket.Conn
	peerConnectionManager *phantomrtc.PhantomPeerManager
	viewportTrack         *webrtc.TrackLocalStaticRTP
	isConnected           bool

	// Buffered channels messages.
	write chan []byte // images and data to client
	read  chan []byte // commands from client
}

// NewClient creates a new client with the given Websocket connection with connected state.
func NewClient(conn *websocket.Conn) *Client {
	client := &Client{}

	client.isConnected = true
	client.write = make(chan []byte)
	client.read = make(chan []byte)
	client.peerConnectionManager = phantomrtc.NewPhantomPeerManager()
	client.conn = conn

	return client
}

//Clear clears and closes all the pointers
func (client *Client) Clear() {
	client.conn = nil
	close(client.read)
	close(client.write)
}

// ClientMap keeps track of all the different Client instances access the clients with the UUID as string
type ClientMap struct {
	mutex   sync.RWMutex
	clients map[string]*Client
}

// NewClientMap creates a newly initialized Maps
func NewClientMap() *ClientMap {
	clientMap := &ClientMap{}
	clientMap.mutex = sync.RWMutex{}
	clientMap.clients = make(map[string]*Client)
	return clientMap
}

// AddClient adds the given client as an entry in the clients map.
func (clientMap *ClientMap) AddClient(uuid string, client *Client) error {
	clientMap.mutex.Lock()
	defer clientMap.mutex.Unlock()

	_, ok := clientMap.clients[uuid]
	if ok {
		return errors.New("Client already exist")
	}

	clientMap.clients[uuid] = client
	return nil
}

// RemoveClient removes the entry to the uuid
func (clientMap *ClientMap) RemoveClient(uuid string) (*Client, error) {
	clientMap.mutex.Lock()
	defer clientMap.mutex.Unlock()
	client, ok := clientMap.clients[uuid]

	if !ok {
		return nil, errors.New("Client doesn't exist")
	}

	delete(clientMap.clients, uuid)
	return client, nil
}

// GetClient returns the desired Client with the saem UUID
func (clientMap *ClientMap) GetClient(uuid string) (*Client, error) {
	clientMap.mutex.RLock()
	defer clientMap.mutex.RUnlock()

	client, ok := clientMap.clients[uuid]

	if !ok {
		return nil, errors.New("Client doesn't exist")
	}

	return client, nil
}

// streamReader reads messages from the websocket connection and fowards them to the read channel
func (client *Client) streamReader() {
	defer func() {
		client.conn.Close()
	}()
	client.conn.SetReadLimit(maxMessageSize)
	client.conn.SetReadDeadline(time.Now().Add(readTimeout))
	// SetPongHandler sets the handler for pong messages received from the peer.
	client.conn.SetPongHandler(func(string) error { client.conn.SetReadDeadline(time.Now().Add(readTimeout)); return nil })
	for {
		if !client.isConnected {
			return
		}

		_, message, err := client.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
		}
		// feed message to command channel
		client.read <- message
	}
}

// streamWriter writes messages from the write channel to the websocket connection
func (client *Client) streamWriter() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		client.conn.Close()
	}()
	for {
		// Go’s select lets you wait on multiple channel operations.
		// We’ll use select to await both of these values simultaneously.
		select {
		case message, ok := <-client.write:
			client.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if !ok {
				client.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			// NextWriter returns a writer for the next message to send.
			// The writer's Close method flushes the complete message to the network.
			w, err := client.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued messages to the current websocket message
			n := len(client.write)
			for i := 0; i < n; i++ {
				w.Write(<-client.write)
			}

			if err := w.Close(); err != nil {
				return
			}

		//a channel that will send the time with a period specified by the duration argument
		case <-ticker.C:
			// SetWriteDeadline sets the deadline for future Write calls
			// and any currently-blocked Write call.
			// Even if write times out, it may return n > 0, indicating that
			// some of the data was successfully written.
			client.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// serveWebsocket handles websocket requests from the peer.
func (clientMap *ClientMap) serveWebsocket(c *gin.Context) {
	sessionID := uuid.NewV4()
	// upgrade connection to websocket
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Println(err)
		return
	}
	conn.EnableWriteCompression(true)

	client := NewClient(conn)

	// get scene width and height from url query params
	// default to 800 if they are not set
	//height := getParameterDefault(c, "h", 800)
	//width := getParameterDefault(c, "w", 800)
	height := 720
	width := 1366

	clientMap.AddClient(sessionID.String(), client)
	fmt.Println("Client Added")

	// run 3d application in separate go routine
	udpsinkPort := 5004
	go renderer.LoadRenderingApp(&client.app, sessionID.String(), height, width, client.write, client.read, udpsinkPort, func() {
		//connection set to False and disconnect the peerConnection
		client.isConnected = false
		client.peerConnectionManager.CloseAll()
	})

	go client.createAndWriteTrack(udpsinkPort)

	// run reader and writer in two different go routines
	// so they can act concurrently
	go client.streamReader()
	go client.streamWriter()

	//offer the track when the peer becomes connected and the render track becomes available
	//go client.offerTracktoPeer(peerConnection)
}

// loadModel loads GLTF model
func (clientMap *ClientMap) loadModel(c *gin.Context) {

	idString := getUUID(c)
	if idString == "" {
		c.JSON(401, "Invalid String")
		return
	}

	client, err := clientMap.GetClient(idString)
	if err != nil {
		c.JSON(401, "Client does not exist")
		return
	}

	client.app.SendMessageToClient("Load Model", "Called")

	file, fileheader, err := c.Request.FormFile("file")

	defer file.Close()
	if err != nil {
		return
	}

	out, err := os.Create(fileheader.Filename)
	if err != nil {
		c.JSON(401, "")
		return
	}
	defer out.Close()
	_, err = io.Copy(out, file)
	if err != nil {
		return
	}

	client.app.LoadScene(fileheader.Filename)

	os.Remove(out.Name())
	return
}

// getObjects returns the list of mesh entity in the rendering scene
func (clientMap *ClientMap) getObjects(c *gin.Context) {

	idString := getUUID(c)
	if idString == "" {
		c.JSON(401, "Invalid String")
		return
	}

	client, err := clientMap.GetClient(idString)
	if err != nil {
		c.JSON(401, "Client does not exist")
		return
	}

	client.app.SendMessageToClient("Get Objects", "Called")

	collection := new(renderer.EntityCollection)

	entityList := client.app.GetEntityList()
	index := 1
	for key, node := range entityList {
		entity := new(renderer.Entity)

		entity.Name = key
		entity.Visible = node.Visible()
		entity.ID = index

		collection.Collection = append(collection.Collection, *entity)

		index++
	}

	c.JSON(200, collection)
	return
}

// CleanClient cleans the client from the disconnected clients.
func CleanClient(clientMap *ClientMap) {
	ticker := time.NewTicker(2 * time.Minute)
	defer func() {
		ticker.Stop()
	}()

	for {
		select {
		case <-ticker.C:
			disConnectedIDs := make([]string, len(clientMap.clients))

			clientMap.mutex.RLock()
			//check for all disconnected clients
			for id, client := range clientMap.clients {
				if !client.isConnected {
					disConnectedIDs = append(disConnectedIDs, id)
				}
			}
			clientMap.mutex.Unlock()

			//clean up all disconnected clients
			for _, clientID := range disConnectedIDs {
				if clientID != "" {
					client, err := clientMap.RemoveClient(clientID)
					if err != nil {
						continue
					}

					client.Clear()
				}
			}
		}
	}

}

// getParameterDefault gets a parameter and returns default value if its not set
func getParameterDefault(c *gin.Context, name string, defaultValue int) int {
	val, err := strconv.Atoi(c.Request.URL.Query().Get(name))
	if err != nil {
		log.Println(err)
		return defaultValue
	}
	return val
}

// getUUID returns the UUID parameter passed to each Rest call
func getUUID(c *gin.Context) string {
	return c.Request.URL.Query().Get("uuid")
}

// createAndWriteTrack will listen to the image in the UDP track
func (client *Client) createAndWriteTrack(udpsinkPort int) {
	// Open a UDP Listener for RTP Packets on port 5004
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: udpsinkPort})
	if err != nil {
		panic(err)
	}
	defer func() {
		if err = listener.Close(); err != nil {
			panic(err)
		}
	}()

	fmt.Println("Waiting for RTP Packets, please run GStreamer or ffmpeg now")

	// Listen for a single RTP Packet, we need this to determine the SSRC
	inboundRTPPacket := make([]byte, 4096) // UDP MTU
	n, _, err := listener.ReadFromUDP(inboundRTPPacket)
	if err != nil {
		panic(err)
	}

	// Unmarshal the incoming packet
	packet := &rtp.Packet{}
	if err = packet.Unmarshal(inboundRTPPacket[:n]); err != nil {
		panic(err)
	}

	// Create a video track
	client.viewportTrack, err = webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: "video/vp8"}, "video", "pion")
	if err != nil {
		panic(err)
	}
	fmt.Println("Track Created")

	// Read RTP packets forever and send them to the localTrack which would then be added to each peer connections
	for {

		//once client becomes disconnected, just return from this function
		if !client.isConnected {
			client.viewportTrack = nil
			break
		}

		n, _, err := listener.ReadFrom(inboundRTPPacket)
		if err != nil {
			fmt.Printf("error during read: %s", err)
			panic(err)
		}

		if _, writeErr := client.viewportTrack.Write(inboundRTPPacket[:n]); writeErr != nil {
			panic(writeErr)
		}
	}
}

// Handle incoming websockets
func (clientMap *ClientMap) createRTCPeerConnection(c *gin.Context) {

	idString := getUUID(c)
	if idString == "" {
		fmt.Println("Invalid UUID")
		c.JSON(401, "Invalid String")
		return
	}

	fmt.Println("Given UUID is", idString)

	client, err := clientMap.GetClient(idString)
	if err != nil {
		fmt.Println("Client Does Not exist")
		c.JSON(401, "Client does not exist")
		return
	}

	// Upgrade HTTP request to Websocket
	// upgrade connection to websocket
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		fmt.Println("Websocket Upgrader failed")
		log.Println(err)
		return
	}

	peerConnection, err := client.peerConnectionManager.CreateNewConnection(conn)
	if err != nil {
		fmt.Println("Peer Connection problem")
		log.Println(err)
		return
	}

	defer peerConnection.Close()
	client.peerConnectionManager.SignalPeerConnections(client.viewportTrack)

	fmt.Println("Something in RTC going on")

	peerConnection.RunWebsocket()
}
