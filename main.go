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

type card int

type game struct {
	mu         sync.Mutex
	count      int
	deck       []card
	playerBust bool
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
{{if .Bust}}<p style="color: red">Bust!</p>{{end}}
</body></html>`

	resultTemplate = `<html><body>
		<h1>Final Result</h1>
		<h2>Your Cards (Score: {{.PlayerScore}})</h2>
		{{range .PlayerHand}}<p>{{.}}</p>{{end}}
		<h2>Dealer's Cards (Score: {{.DealerScore}})</h2>
		{{range .DealerHand}}<p>{{.}}</p>{{end}}
		<p><strong>Outcome:</strong> {{.Outcome}}</p>
	</body></html>`
)

var (
	values    = []string{"2", "3", "4", "5", "6", "7", "8", "9", "10", "Jack", "Queen", "King", "Ace"}
	scores    = []int{2, 3, 4, 5, 6, 7, 8, 9, 10, 10, 10, 10, 11}
	suits     = []string{"spades", "hearts", "diamonds", "clubs"}
	games     = make(map[string]*game)
	gameMutex sync.RWMutex
	gameIDRe  = regexp.MustCompile(`^game-\d+$`)
)

func generateGameID() string {
	gameMutex.Lock()
	defer gameMutex.Unlock()
	return fmt.Sprintf("game-%d", time.Now().UnixNano())
}

func newGame() *game {
	deck := make([]card, 52)
	for i := range deck {
		deck[i] = card(i)
	}
	rand.New(rand.NewSource(time.Now().UnixNano())).Shuffle(len(deck), func(i, j int) {
		deck[i], deck[j] = deck[j], deck[i]
	})
	return &game{
		deck: deck,
	}
}

func (c card) String() string {
	return fmt.Sprintf("%v of %v", values[int(c)%len(values)], suits[int(c)%len(suits)])
}

func (c card) score() int {
	return scores[int(c)%len(scores)]
}

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

func newGameHandler(w http.ResponseWriter, r *http.Request) {
	gameID := generateGameID()
	newGame := newGame()

	gameMutex.Lock()
	games[gameID] = newGame
	gameMutex.Unlock()

	http.Redirect(w, r, "/"+gameID+"/hit", http.StatusFound)
}

func validateGameID(gameID string) bool {
	return gameIDRe.MatchString(gameID)
}

func hitHandler(w http.ResponseWriter, r *http.Request) {
	gameKey := r.PathValue("game")
	if !validateGameID(gameKey) {
		http.Error(w, "Invalid game ID", http.StatusBadRequest)
		return
	}

	gameMutex.RLock()
	game, ok := games[gameKey]
	gameMutex.RUnlock()

	if !ok {
		http.Error(w, "Game not found", http.StatusNotFound)
		return
	}

	game.mu.Lock()
	defer game.mu.Unlock()

	if game.playerBust || game.count >= len(game.deck)-1 {
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}

	game.count++
	playerHand := game.deck[1 : 1+game.count]
	playerScore := score(playerHand)

	if playerScore > 21 {
		game.playerBust = true
	}

	if isBlackjack(playerHand) {
		standHandler(w, r)
		return
	}

	t := template.Must(template.New("game").Parse(gamePage))
	t.Execute(w, map[string]interface{}{
		"dealer": game.deck[0],
		"you":    playerHand,
		"Bust":   playerScore > 21,
	})
}

func standHandler(w http.ResponseWriter, r *http.Request) {
	gameKey := r.PathValue("game")
	if !validateGameID(gameKey) {
		http.Error(w, "Invalid game ID", http.StatusBadRequest)
		return
	}

	gameMutex.RLock()
	game, ok := games[gameKey]
	gameMutex.RUnlock()

	if !ok {
		http.Error(w, "Game not found", http.StatusNotFound)
		return
	}

	game.mu.Lock()
	defer game.mu.Unlock()
	defer func() {
		gameMutex.Lock()
		delete(games, gameKey)
		gameMutex.Unlock()
	}()

	if game.count < 2 {
		http.Error(w, "Not enough cards to stand", http.StatusBadRequest)
		return
	}

	playerHand := game.deck[1 : 1+game.count]
	playerScore := score(playerHand)

	dealerHand := []card{game.deck[0]}
	dealerScore := 0

	for i := 1 + game.count; i < len(game.deck); i++ {
		dealerHand = append(dealerHand, game.deck[i])
		dealerScore = score(dealerHand)

		// Implement Soft 17 rule
		if dealerScore == 17 && hasAce(dealerHand) {
			continue
		}
		if dealerScore >= 17 {
			break
		}
	}

	outcome := determineOutcome(playerScore, dealerScore)

	t := template.Must(template.New("result").Parse(resultTemplate))
	t.Execute(w, map[string]interface{}{
		"PlayerHand":  playerHand,
		"PlayerScore": playerScore,
		"DealerHand":  dealerHand,
		"DealerScore": dealerScore,
		"Outcome":     outcome,
	})
}

func determineOutcome(player, dealer int) string {
	switch {
	case player > 21:
		return "You bust!"
	case dealer > 21:
		return "Dealer busts! You win!"
	case player == dealer:
		return "Push!"
	case player == 21:
		return "Blackjack! You win!"
	case player > dealer:
		return "You win!"
	default:
		return "You lose!"
	}
}

func welcomeHandler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte(welcomePage))
}

func main() {
	http.HandleFunc("/", welcomeHandler)
	http.HandleFunc("/new", newGameHandler)
	http.HandleFunc("/{game}/stand", standHandler)
	http.HandleFunc("/{game}/hit", hitHandler)
	fmt.Println("Starting server on http://localhost:8080")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		return
	}
}
