package ui

import (
	"errors"
	"image"
	"time"

	"gioui.org/app"
	"gioui.org/io/key"
	"gioui.org/io/system"
	"gioui.org/layout"

	"github.com/raedahgroup/dcrlibwallet"
	"github.com/raedahgroup/godcr/ui/decredmaterial"
	"github.com/raedahgroup/godcr/wallet"
)

// Window represents the app window (and UI in general). There should only be one.
// Window uses an internal state of booleans to determine what the window is currently displaying.
type Window struct {
	window *app.Window
	theme  *decredmaterial.Theme
	gtx    *layout.Context

	wallet             *wallet.Wallet
	walletInfo         *wallet.MultiWalletInfo
	walletSyncStatus   *wallet.SyncStatus
	walletTransactions *wallet.Transactions
	walletTransaction  *wallet.Transaction

	current string
	dialog  layout.Widget
	tabs    *decredmaterial.Tabs

	signatureResult *wallet.Signature

	selectedAccount int
	txAuthor        dcrlibwallet.TxAuthor
	broadcastResult wallet.Broadcast

	selected int
	states

	inputs

	err string

	combined
	outputs
	pages     map[string]layout.Widget
	keyEvents chan *key.Event
}

// CreateWindow creates and initializes a new window with start
// as the first page displayed.
// Should never be called more than once as it calls
// app.NewWindow() which does not support being called more
// than once.
func CreateWindow(wal *wallet.Wallet, decredIcons map[string]image.Image) (*Window, error) {
	win := new(Window)
	win.window = app.NewWindow(app.Title("godcr"))
	theme := decredmaterial.NewTheme()
	if theme == nil {
		return nil, errors.New("Unexpected error while loading theme")
	}
	win.theme = theme
	win.gtx = layout.NewContext(win.window.Queue())

	win.walletInfo = new(wallet.MultiWalletInfo)
	win.walletSyncStatus = new(wallet.SyncStatus)
	win.walletTransactions = new(wallet.Transactions)

	win.wallet = wal
	win.states.loading = true
	win.tabs = decredmaterial.NewTabs()
	win.tabs.Position = decredmaterial.Left
	win.current = PageOverview
	win.dialog = func() {}
	win.keyEvents = make(chan *key.Event)

	win.initWidgets()
	win.addPages(decredIcons)
	return win, nil
}

func (win *Window) unloaded() {
	lbl := win.theme.H3("Multiwallet not loaded\nIs another instance open?")
	for {
		e := <-win.window.Events()
		switch evt := e.(type) {
		case system.DestroyEvent:
			return
		case system.FrameEvent:
			win.gtx.Reset(evt.Config, evt.Size)
			lbl.Layout(win.gtx)
			evt.Frame(win.gtx.Ops)
		}
	}
}

// Loop runs main event handling and page rendering loop
func (win *Window) Loop(shutdown chan int) {
	for {
		select {
		case e := <-win.wallet.Send:
			if e.Err != nil {
				err := e.Err.Error()
				log.Error("Wallet Error: " + err)
				if err == dcrlibwallet.ErrWalletDatabaseInUse {
					close(shutdown)
					win.unloaded()
					return
				}
				win.err = err
				if win.states.loading {
					log.Warn("Attemping to get multiwallet info")
					win.wallet.GetMultiWalletInfo()
				}

				win.window.Invalidate()
				break
			}
			win.updateStates(e.Resp)

		case update := <-win.wallet.Sync:
			switch update.Stage {
			case wallet.SyncCompleted:
				win.updateSyncStatus(false, true)
			case wallet.SyncStarted:
				// dcrlibwallet triggers the SyncStart method several times
				// without sending a SyncComplete signal when sync is done.
				if !win.walletInfo.Synced {
					win.updateSyncStatus(true, false)
				}
			case wallet.SyncCanceled:
				win.updateSyncStatus(false, false)
			case wallet.HeadersFetchProgress:
				win.updateSyncProgress(update.ProgressReport)
			case wallet.AddressDiscoveryProgress:
				win.updateSyncProgress(update.ProgressReport)
			case wallet.HeadersRescanProgress:
				win.updateSyncProgress(update.ProgressReport)
			case wallet.PeersConnected:
				win.updateConnectedPeers(update.ConnectedPeers)
			case wallet.BlockAttached:
				if win.walletInfo.Synced {
					win.wallet.GetMultiWalletInfo()
					win.updateSyncProgress(update.BlockInfo)
				}
			case wallet.BlockConfirmed:
				win.updateSyncProgress(update.ConfirmedTxn)
			}

		case e := <-win.window.Events():
			switch evt := e.(type) {
			case system.DestroyEvent:
				close(shutdown)
				return
			case system.FrameEvent:
				win.gtx.Reset(evt.Config, evt.Size)
				ts := int64(time.Since(time.Unix(win.walletInfo.BestBlockTime, 0)).Seconds())
				win.walletInfo.LastSyncTime = wallet.SecondsToDays(ts)
				s := win.states
				if win.walletInfo.LoadedWallets == 0 {
					win.current = PageCreateRestore
				}

				if s.loading {
					win.Loading()
				} else if s.dialog {
					decredmaterial.Modal{Direction: layout.Center}.Layout(win.gtx, func() {
						win.theme.Background(win.gtx, win.pages[win.current])
					}, win.dialog)
				} else {
					win.theme.Background(win.gtx, win.pages[win.current])
				}

				win.HandleInputs()
				evt.Frame(win.gtx.Ops)
			case key.Event:
				go func() {
					win.keyEvents <- &evt
				}()
			case nil:
				// Ignore
			default:
				log.Tracef("Unhandled window event %+v\n", e)
			}
		}
	}
}
