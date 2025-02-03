package main

import (
	"fmt"
	"html/template"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

type card int

type game struct {
	mu    sync.Mutex
	count int
	deck  []card
}

const (
	welcomePage = `<html><body> 
<h1>Blackjack</h1>
<a href="/new">Start</a>
</body></html>`

	gamePage = `<html><body> 
<a href="hit">Hit</a> <a href="stand">Stand</a>
<h1>Dealer</h1>
<p>{{.dealer}}</p>
<p>??</p>
<h1>You</h1>
{{range .you}}<p>{{.}}</p>{{end}}
</body></html>`
)

var (
	values      = []string{"2", "3", "4", "5", "6", "7", "8", "9", "10", "Jack", "Queen", "King", "Ace"}
	scores      = []int{2, 3, 4, 5, 6, 7, 8, 9, 10, 10, 10, 10, 11}
	suits       = []string{"spades", "hearts", "diamonds", "clubs"}
	games       = make(map[string]*game)
	gameMutex   sync.RWMutex
	gameCounter int
)

func generateGameID() string {
	gameMutex.Lock()
	defer gameMutex.Unlock()
	gameCounter++
	return fmt.Sprintf("game-%d", gameCounter)
}

func newGame() *game {
	deck := make([]card, 52)
	for i := range deck {
		deck[i] = card(i)
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	r.Shuffle(len(deck), func(i, j int) {
		deck[i], deck[j] = deck[j], deck[i]
	})
	return &game{
		deck: deck,
	}
}

func main() {
	http.HandleFunc("/", welcomeHandler)
	http.HandleFunc("/new", newGameHandler)
	http.HandleFunc("/{game}/stand", standHandler)
	http.HandleFunc("/{game}/hit", hitHandler)
	fmt.Println("Starting server on http://localhost:8080")
	http.ListenAndServe(":8080", nil)
}

func (c card) String() string {
	return fmt.Sprintf("%v of %v", values[int(c)%len(values)], suits[int(c)%len(suits)])
}

func (c card) score() int {
	return scores[int(c)%len(scores)]
}

func score(hand []card) int {
	var score, aces int
	for _, c := range hand {
		s := c.score()
		score += s
		if s == 11 {
			aces++
		}
	}

	for score > 21 && aces > 0 {
		score -= 10
		aces--
	}
	return score
}

func newGameHandler(w http.ResponseWriter, r *http.Request) {
	gameID := generateGameID()
	newGame := newGame()

	gameMutex.Lock()
	games[gameID] = newGame
	gameMutex.Unlock()

	http.Redirect(w, r, "/"+gameID+"/hit", http.StatusFound)
}

func hitHandler(w http.ResponseWriter, r *http.Request) {
	gameKey := r.PathValue("game")

	gameMutex.RLock()
	game, ok := games[gameKey]
	gameMutex.RUnlock()

	if !ok {
		t := template.Must(template.New("error").Parse("<html><body>Game not found: {{.}}</body></html>"))
		t.Execute(w, template.HTML(gameKey))
		return
	}

	game.mu.Lock()
	defer game.mu.Unlock()

	game.count++
	if game.count >= len(game.deck) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Deck exhausted"))
		return
	}

	t := template.Must(template.New("game").Parse(gamePage))
	t.Execute(w, map[string]interface{}{
		"dealer": game.deck[0],
		"you":    game.deck[1 : 1+game.count],
	})
}

func standHandler(w http.ResponseWriter, r *http.Request) {
	gameKey := r.PathValue("game")

	gameMutex.RLock()
	game, ok := games[gameKey]
	gameMutex.RUnlock()

	if !ok {
		t := template.Must(template.New("error").Parse("<html><body>Game not found: {{.}}</body></html>"))
		t.Execute(w, template.HTML(gameKey))
		return
	}

	// Lock the game for final processing
	game.mu.Lock()
	defer game.mu.Unlock()
	defer func() {
		gameMutex.Lock()
		delete(games, gameKey)
		gameMutex.Unlock()
	}()

	playerHand := game.deck[1 : 1+game.count]
	playerScore := score(playerHand)

	dealerHand := []card{game.deck[0]}
	dealerScore := 0
	for i := 1 + game.count; i < len(game.deck); i++ {
		dealerHand = append(dealerHand, game.deck[i])
		dealerScore = score(dealerHand)
		if dealerScore >= 17 {
			break
		}
	}

	resultPage := `<html><body>
		<h1>Final Result</h1>
		<h2>Your Cards (Score: {{.PlayerScore}})</h2>
		{{range .PlayerHand}}<p>{{.}}</p>{{end}}
		<h2>Dealer's Cards (Score: {{.DealerScore}})</h2>
		{{range .DealerHand}}<p>{{.}}</p>{{end}}
		<p><strong>Outcome:</strong> {{.Outcome}}</p>
	</body></html>`

	outcome := "You lose!"
	switch {
	case playerScore > 21:
		outcome = "You bust!"
	case dealerScore > 21 || playerScore > dealerScore:
		outcome = "You win!"
	case playerScore == dealerScore:
		outcome = "Push!"
	}

	t := template.Must(template.New("result").Parse(resultPage))
	t.Execute(w, map[string]interface{}{
		"PlayerHand":  playerHand,
		"PlayerScore": playerScore,
		"DealerHand":  dealerHand,
		"DealerScore": dealerScore,
		"Outcome":     outcome,
	})
}

func welcomeHandler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte(welcomePage))
}
