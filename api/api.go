package api

import (
  "encoding/json"
  "net/http"
)

// Coin Balance Request Params
type CoinBalanceParams struct {
  Username string
}


// Coin balance Response
type CoinBalanceResponse struct {
  // Success code
  Code int
  // Balance
  Balance int64
}


type Error struct {
  Code int
  Message string
}


