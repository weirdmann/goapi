package internal

import (
"os"
"os/signal"
"syscall"
"log"
)



func GracefulShutdown(log *log.Logger ,cancel_function func()) {
  cancelCh := make( chan os.Signal, 1)
  signal.Notify(cancelCh, syscall.SIGTERM, syscall.SIGINT)
  sig := <- cancelCh
  log.Printf("\n --- Caught signal: %s, calling cancel function --- \n\n", sig)

  cancel_function()
}
