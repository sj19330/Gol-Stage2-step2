package gol

import (
	"flag"
	"fmt"
	"log"
	"net/rpc"
	"time"

	"uk.ac.bris.cs/gameoflife/util"
)

var server *string

// Params provides the details of how to run the Game of Life and which image to load.
type Params struct {
	Turns       int
	Threads     int
	ImageWidth  int
	ImageHeight int
}

type Args struct {
	P     Params
	World [][]byte
	Turn  int
}

type distributorChannels struct {
	events     chan<- Event
	ioCommand  chan<- ioCommand
	ioIdle     <-chan bool
	ioFilename chan<- string
	ioOutput   chan<- uint8
	ioInput    <-chan uint8
	keyPresses <-chan rune
}

type AliveCellsReply struct {
	AliveCells     int
	CompletedTurns int
}

type SaveReply struct {
	CompletedTurns int
	World          [][]byte
}

type PauseReply struct {
	CompletedTurns int
	World          [][]byte
}

func outputPGM(world [][]byte, c distributorChannels, p Params, turn int) {
	c.ioCommand <- ioCommand(ioOutput)
	outputFileName := fmt.Sprintf("%dx%dx%d", p.ImageHeight, p.ImageWidth, turn)
	c.ioFilename <- outputFileName
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			c.ioOutput <- world[y][x]
		}
	}
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle
	c.events <- ImageOutputComplete{turn, outputFileName}
}

func getAliveCells(p Params, world [][]byte) []util.Cell {
	finalAliveCells := make([]util.Cell, 0)
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			if world[y][x] == 255 {
				cell := util.Cell{Y: y, X: x}
				finalAliveCells = append(finalAliveCells, cell)
			}
		}
	}
	return finalAliveCells
}

func ticker(tck *time.Ticker, controller *rpc.Client, c distributorChannels) {
	for range tck.C {
		aliveCellsResponse := new(AliveCellsReply)
		controller.Call("Engine.GetAliveCells", 0, &aliveCellsResponse)

		c.events <- AliveCellsCount{
			CompletedTurns: aliveCellsResponse.CompletedTurns,
			CellsCount:     aliveCellsResponse.AliveCells,
		}
	}
}

func engine(p Params, c distributorChannels, keyPresses <-chan rune) {
	// connect to engine
	controller := engineConnection()
	

	var isAlreadyRunning bool
	controller.Call("Engine.IsAlreadyRunning", p, &isAlreadyRunning)
	
	// create slice to store world
	world := make([][]byte, p.ImageHeight)
	for i := range world {
		world[i] = make([]byte, p.ImageWidth)
	}

	

	if isAlreadyRunning == false {
		c.ioCommand <- ioCommand(ioInput)                             // send read command down command channel
		filename := fmt.Sprintf("%dx%d", p.ImageHeight, p.ImageWidth) // gets file name from putting file dimensions together
		c.ioFilename <- filename                                      // sends file name to the fileName channel

		// populate world
		for y := 0; y < p.ImageHeight; y++ {
			for x := 0; x < p.ImageWidth; x++ {
				world[y][x] = <-c.ioInput
			}
		}
	}

	
	tck := time.NewTicker(2 * time.Second)
	go ticker(tck, controller, c)

	

	go func() {
		for {
			select {
			case pressed := <-keyPresses:
				if pressed == 's' {
					saveResponse := new(SaveReply)
					controller.Call("Engine.Save", 0, &saveResponse)
					outputPGM(saveResponse.World, c, p, saveResponse.CompletedTurns)
				} else if pressed == 'q' {
					var quitResponse int
					controller.Call("Engine.Quit", 0, &quitResponse)
					c.events <- StateChange{CompletedTurns: quitResponse, NewState: Quitting}
					c.ioCommand <- ioCheckIdle
					<-c.ioIdle
					close(c.events)
					return
				} else if pressed == 'p' {
					tck.Stop()

					pauseResponse := new(PauseReply)
					controller.Call("Engine.Pause", 0, &pauseResponse)
					c.events <- StateChange{CompletedTurns: pauseResponse.CompletedTurns, NewState: Paused}

					for {
						tempKey := <-c.keyPresses
						if tempKey == 'p' {
							executeResponse := new(PauseReply)
							controller.Call("Engine.Execute", 0, &executeResponse)
							c.events <- StateChange{CompletedTurns: executeResponse.CompletedTurns, NewState: Executing}

							tck := time.NewTicker(2 * time.Second)
							go ticker(tck, controller, c)

							break
						}
					}
				}
			default:
			}
		}
	}()

	// args := world
	response := new([][]byte)
	

	if isAlreadyRunning == false {
		request := Args{
			World: world,
			P:     p,
		}
		controller.Call("Engine.Start", request, &response)
		world = *response
	}else{
		controller.Call("Engine.Continue", 0, &response)
		world = *response
	}

	tck.Stop()

	finalAliveCellsNum := getAliveCells(p, world)
	c.events <- FinalTurnComplete{p.Turns, finalAliveCellsNum}
	outputPGM(world, c, p, p.Turns)

	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{p.Turns, Quitting}
	close(c.events)
}

func engineConnection() *rpc.Client {
	// connect to engine
	if server == nil {
		server = flag.String("server", "127.0.0.1:8030", "IP:port string to connect to as server")
	}
	controller, error := rpc.Dial("tcp", *server)

	if error != nil {
		log.Fatal("Unable to connect", error)
	}
	return controller

}

// Run starts the processing of Game of Life. It should initialise channels and goroutines.
func Run(p Params, events chan<- Event, keyPresses <-chan rune) {
	ioCommand := make(chan ioCommand)
	ioIdle := make(chan bool)
	ioFileName := make(chan string)
	ioOutput := make(chan uint8)
	ioInput := make(chan uint8)

	distributorChannels := distributorChannels{
		events,
		ioCommand,
		ioIdle,
		ioFileName,
		ioOutput,
		ioInput,
		keyPresses,
	}

	go engine(p, distributorChannels, keyPresses)

	ioChannels := ioChannels{
		command:  ioCommand,
		idle:     ioIdle,
		filename: ioFileName,
		output:   ioOutput,
		input:    ioInput,
	}
	go startIo(p, ioChannels)
}
