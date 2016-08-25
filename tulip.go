package main

import (
	"log"
	"fmt"
	"os"
	"strings"
	"bytes"
	"sync"
	"net/http"
	"crypto/rand"
	//"encoding/base64"
	"encoding/hex"
	"golang.org/x/net/websocket"
	"golang.org/x/crypto/ssh/terminal"
	"github.com/twstrike/otr3"
)

const OTRFragmentSize = 140

type Person struct {
        OTRFingerprints         [][]byte
        OnionAddresses          []string
}

type AddressBook map[string]Person

func LookUpAddressBookByFingerprint(abook *AddressBook, FP []byte) (name string) {
	for name, person := range *abook {
		for _, fp := range person.OTRFingerprints {
			if bytes.Equal(fp, FP) {
				return name
			}
		}
	}
	return name
}


type Talk struct {
	Conversation	*otr3.Conversation
	WebSocket	*websocket.Conn
	wg		*sync.WaitGroup
	toSend		[]otr3.ValidMessage
	incoming	[]string
	outgoing	[]string
	finished	bool
}

func getBestName(talk *Talk) (name string) {
	fp := talk.Conversation.GetTheirKey().Fingerprint()
	name = LookUpAddressBookByFingerprint(&addressBook, fp)
	if (name=="") {
		name = fmt.Sprintf("%x", fp)
	}
	return name
}

var (
	privKey		*otr3.DSAPrivateKey
	addressBook =	make(AddressBook)
	activeTalks	[]*Talk
	CurrentTalk	*Talk
	ToTerm		chan string
)

func OTRReceive(talk *Talk) {
	data := make([]byte, 512)
	for !talk.finished {
		n, err := talk.WebSocket.Read(data)
		if err != nil {
			goto Finish
		}
		msg, toSend, err := talk.Conversation.Receive(data[:n])
		if err != nil {
			log.Printf("Unable to recieve OTR message: %v", err)
		}
		talk.toSend = append(talk.toSend, toSend...)
		if len(msg) > 0 {
			talk.incoming = append(talk.incoming, string(msg))
			toTerm := fmt.Sprintf("%s: %s", getBestName(talk), talk.incoming[0])
			log.Printf("%s", toTerm)
			talk.incoming = talk.incoming[1:]

		}
	}
   Finish:
	talk.finished = true
	talk.wg.Done()
}
func OTRSend(talk *Talk) {
	for !talk.finished {
		if len(talk.outgoing) > 0 {
			outMsg := talk.outgoing[0]
			talk.outgoing = talk.outgoing[1:]
			toSend, err := talk.Conversation.Send(otr3.ValidMessage(outMsg))
			if err != nil {
				log.Printf("Unable to process an outgoing message: %v", err)
			}
			if (len(outMsg) > 0) {
				log.Printf("> %s", outMsg)
			}
			talk.toSend = append(talk.toSend, toSend...)
		}
		for (len(talk.toSend) > 0) {
			_, err := talk.WebSocket.Write(talk.toSend[0])
			if err != nil {
				goto Finish
			}
			talk.toSend = talk.toSend[1:]
		}
	}
   Finish:
	talk.finished = true
	talk.wg.Done()
}

func StartTalk(ws *websocket.Conn) {
	talk := &Talk{}
	activeTalks = append(activeTalks, talk)
	CurrentTalk = talk
	talk.Conversation = &otr3.Conversation{}
	talk.Conversation.SetOurKeys([]otr3.PrivateKey{privKey})
	talk.Conversation.Policies.RequireEncryption()
	//c.Policies.AllowV2()
	talk.Conversation.Policies.AllowV3()
	talk.WebSocket = ws
	defer ws.Close()

	var wg sync.WaitGroup
	talk.wg = &wg
	wg.Add(2)

	go OTRReceive(talk)
	go OTRSend(talk)

	wg.Wait()
	log.Printf("Ended talk")
}
func IncomingTalkHandler(ws *websocket.Conn) {
	log.Printf("Got new connection")
	StartTalk(ws)
}


func main() {
	log.Printf("Welcome to tulip!")


	privKey = &otr3.DSAPrivateKey{}
	privKey.Generate(rand.Reader)
	//log.Printf(base64.RawStdEncoding.EncodeToString(privKey.Serialize(nil)))
	log.Println("Our fingerprint:", hex.EncodeToString(privKey.Fingerprint()))

	browserFP, _ := hex.DecodeString("2264d806e7789a5773bdaffb798bcf3fdb456a81")
	browserP := Person{OTRFingerprints: [][]byte{browserFP}}
	addressBook["browser"] = browserP
	log.Print(addressBook)



	http.Handle("/tulip", websocket.Handler(IncomingTalkHandler))
	http.Handle("/", http.FileServer(http.Dir("webroot")))

	go http.ListenAndServe(":8000", nil)
/*
	showIncoming := func() {
		for {
		}
	}
	go showIncoming()
*/
	oldState, err := terminal.MakeRaw(0)
	if err != nil {
		panic(err)
	}
	defer terminal.Restore(0, oldState)

	term := terminal.NewTerminal(os.Stdin, "")
	go func() {
		for {
			toTerm := <-ToTerm
			log.Printf("%s", toTerm)
			//term.Write([]byte(<-toTerm))
		}
	}()

	for {
		term.SetPrompt("> ")
		input, err := term.ReadLine()
		if err != nil {
			log.Fatalf("Unable to read line from terminal: %v", err)
		}
		if strings.HasPrefix(input, "/") {
			cmdLine := strings.TrimPrefix(input, "/")
			args := strings.Split(cmdLine, " ")
			switch args[0] {
			case "list":
				for _, talk := range activeTalks {
					log.Printf("[*] %s", getBestName(talk))
				}
			case "connect":
				if !strings.HasSuffix(args[1], ".onion") { //check existence!
					log.Printf("It's not an onion address.")
					break
				}
				onionAddress := args[1]
				origin := "http://"+onionAddress+"/"
				url := "ws://"+onionAddress+"/tulip"
				ws, err := websocket.Dial(url, "", origin)
				if err != nil {
					log.Printf("Unable to connect")
					break
				}
				go StartTalk(ws)
			default:
				log.Printf("No such command.")
			}
			continue
		}
		if (CurrentTalk != nil) {
			CurrentTalk.outgoing = append(CurrentTalk.outgoing, input)
		} else {
			log.Printf("There is no active talk.")
		}
	}
}
