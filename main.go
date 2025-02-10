package main

import (
	"fmt"
	"html/template"
	"math/rand"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// Card represents a playing card with rank and suit
type card int

// GameState contains all game state information
type GameState struct {
	PlayerHand []card
	DealerHand []card
	Deck       []card
	Bust       bool
	Stand      bool
	Message    string
}

// GameCommand represents actions sent to game goroutine
type GameCommand struct {
	Action   string            // Message type
	Response chan<- *GameState // Reply channel
}

// GameSession manages communication with game goroutine
type GameSession struct {
	commands chan GameCommand // Message queue
	created  time.Time        // Actor state
}

var (
	values = []string{"2", "3", "4", "5", "6", "7", "8", "9", "10", "Jack", "Queen", "King", "Ace"}
	scores = []int{2, 3, 4, 5, 6, 7, 8, 9, 10, 10, 10, 10, 11}
	suits  = []string{"spades", "hearts", "diamonds", "clubs"}

	games     = make(map[string]*GameSession)
	gameMutex sync.RWMutex
	gameIDRe  = regexp.MustCompile(`^game-\d+$`)
	funcMap   = template.FuncMap{
		"score": score}

	templates = template.Must(template.New("").Funcs(funcMap).Parse(`
	{{define "game"}}
	<html><body>
		<h1>Blackjack</h1>
		{{if .Message}}<p style="color:red">{{.Message}}</p>{{end}}
		<h2>Dealer's Hand</h2>
		<p>{{index .DealerHand 0}} + ???</p>
		
		<h2>Your Hand ({{score .PlayerHand}})</h2>
		{{range .PlayerHand}}<p>{{.}}</p>{{end}}
		
		{{if not .Stand}}
		<a href="/game/{{.GameID}}/hit">Hit</a>
		<a href="/game/{{.GameID}}/stand">Stand</a>
		{{else}}
		<h2>Dealer's Full Hand ({{score .DealerHand}})</h2>
		{{range .DealerHand}}<p>{{.}}</p>{{end}}
		<h3>{{.Message}}</h3>
		<a href="/">New Game</a>
		{{end}}
	</body></html>
	{{end}}
`))
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func (c card) String() string {
	return fmt.Sprintf("%v of %v", values[int(c)%len(values)], suits[int(c)%len(suits)])
}

func (c card) score() int {
	return scores[int(c)%len(scores)]
}

// score calculates the best possible score for a hand
func score(hand []card) int {
	var total, aces int
	for _, c := range hand {
		s := c.score()
		total += s
		if s == 11 {
			aces++
		}
	}

	for total > 21 && aces > 0 {
		total -= 10
		aces--
	}
	return total
}

func isBlackjack(hand []card) bool {
	return len(hand) == 2 && score(hand) == 21
}

func hasAce(hand []card) bool {
	for _, c := range hand {
		if c.score() == 11 {
			return true
		}
	}
	return false
}

// gameLoop runs the game state machine
func gameLoop(initial GameState) *GameSession {
	session := &GameSession{
		commands: make(chan GameCommand),
		created:  time.Now(),
	}

	go func() {
		state := initial
		defer close(session.commands)

		for cmd := range session.commands {
			switch cmd.Action {
			case "hit":
				if !state.Stand && !state.Bust {
					state.PlayerHand = append(state.PlayerHand, state.Deck[0])
					state.Deck = state.Deck[1:]
					if score(state.PlayerHand) > 21 {
						state.Bust = true
						state.Message = "Bust!"
					}
					if isBlackjack(state.PlayerHand) {
						state.Stand = true
						state.Message = "Blackjack! You win!"
					}

				}
			case "stand":
				if !state.Stand && !state.Bust {
					state.Stand = true
					dealerScore := score(state.DealerHand)
					// Dealer logic
					for dealerScore < 17 || (dealerScore == 17 && hasAce(state.DealerHand)) {
						state.DealerHand = append(state.DealerHand, state.Deck[0])
						state.Deck = state.Deck[1:]
						dealerScore = score(state.DealerHand)
					}

					// Determine winner
					playerScore := score(state.PlayerHand)
					switch {
					case dealerScore > 21:
						state.Message = "Dealer busts! You win!"
					case playerScore > dealerScore:
						state.Message = "You win!"
					case playerScore == dealerScore:
						state.Message = "Push!"
					case playerScore == 21:
						state.Message = "Blackjack! You win!"
					default:
						state.Message = "You lose!"
					}
				}
			}
			cmd.Response <- &state
		}
	}()

	return session
}

func getSession(gameID string) (*GameSession, bool) {
	gameMutex.RLock()
	defer gameMutex.RUnlock()
	session, exists := games[gameID]
	return session, exists
}

func createGame() (string, *GameSession) {
	gameID := fmt.Sprintf("game-%d", time.Now().UnixNano())
	deck := make([]card, 52)
	for i := range deck {
		deck[i] = card(i)
	}
	rand.Shuffle(len(deck), func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })

	initialState := GameState{
		PlayerHand: []card{deck[0]},
		DealerHand: []card{deck[1]},
		Deck:       deck[2:],
	}

	session := gameLoop(initialState)

	gameMutex.Lock()
	defer gameMutex.Unlock()
	games[gameID] = session
	return gameID, session
}

func gameHandler(w http.ResponseWriter, r *http.Request, action string) {
	gameID := r.PathValue("game")
	if !gameIDRe.MatchString(gameID) {
		http.Error(w, "Invalid game ID", http.StatusBadRequest)
		return
	}

	session, exists := getSession(gameID)
	if !exists {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	response := make(chan *GameState)
	session.commands <- GameCommand{Action: action, Response: response}

	select {
	case state := <-response:
		data := struct {
			*GameState
			GameID string
		}{
			GameState: state,
			GameID:    gameID,
		}
		templates.ExecuteTemplate(w, "game", data)
	case <-time.After(2 * time.Second):
		http.Error(w, "Game timeout", http.StatusGatewayTimeout)
	}
}

func hitHandler(w http.ResponseWriter, r *http.Request) {
	gameHandler(w, r, "hit")
}

func standHandler(w http.ResponseWriter, r *http.Request) {
	gameHandler(w, r, "stand")
}

func newHandler(w http.ResponseWriter, r *http.Request) {
	gameID, session := createGame()
	response := make(chan *GameState)
	session.commands <- GameCommand{Action: "", Response: response}
	<-response // Wait for initial state
	http.Redirect(w, r, "/game/"+gameID+"/hit", http.StatusSeeOther)
}

func cleanupOldGames() {
	gameMutex.Lock()
	defer gameMutex.Unlock()

	for id, session := range games {
		if time.Since(session.created) > 30*time.Minute {
			close(session.commands)
			delete(games, id)
		}
	}
}

func main() {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			cleanupOldGames()
		}
	}()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/new", http.StatusSeeOther)
	})
	http.HandleFunc("/new", newHandler)
	http.HandleFunc("/game/{game}/hit", hitHandler)
	http.HandleFunc("/game/{game}/stand", standHandler)

	fmt.Println("Server running on :8080")
	http.ListenAndServe(":8080", nil)
}
