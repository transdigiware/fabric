/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package commit

import (
	"sync"

	"github.com/hyperledger/fabric/core/ledger"
)

type channelLevelNotifier struct {
	commitChannel <-chan *ledger.CommitNotification
	done          <-chan struct{}
	lock          sync.Mutex
	listeners     map[string][]*transactionListener
	closed        bool
}

func newChannelNotifier(done <-chan struct{}, commitChannel <-chan *ledger.CommitNotification) *channelLevelNotifier {
	notifier := &channelLevelNotifier{
		commitChannel: commitChannel,
		listeners:     make(map[string][]*transactionListener),
		done:          done,
	}
	go notifier.run()
	return notifier
}

func (notifier *channelLevelNotifier) run() {
	for {
		select {
		case blockCommit, ok := <-notifier.commitChannel:
			if !ok {
				notifier.close()
				return
			}
			notifier.removeCompletedListeners()
			notifier.receiveBlock(blockCommit)
		case <-notifier.done:
			notifier.close()
			return
		}
	}
}

func (notifier *channelLevelNotifier) receiveBlock(blockCommit *ledger.CommitNotification) {
	for transactionID, status := range blockCommit.TxIDValidationCodes {
		notification := &Notification{
			BlockNumber:    blockCommit.BlockNumber,
			TransactionID:  transactionID,
			ValidationCode: status,
		}
		notifier.notify(notification)
	}
}

func (notifier *channelLevelNotifier) removeCompletedListeners() {
	notifier.lock.Lock()
	defer notifier.lock.Unlock()

	for key, listeners := range notifier.listeners {
		for i := 0; i < len(listeners); {
			if !listeners[i].isDone() {
				i++
				continue
			}

			listeners[i].close()

			lastIndex := len(listeners) - 1
			listeners[i] = listeners[lastIndex]
			listeners = listeners[:lastIndex]
		}

		if len(listeners) > 0 {
			notifier.listeners[key] = listeners
		} else {
			delete(notifier.listeners, key)
		}
	}
}

func (notifier *channelLevelNotifier) notify(notification *Notification) {
	notifier.lock.Lock()
	defer notifier.lock.Unlock()

	for _, listener := range notifier.listeners[notification.TransactionID] {
		listener.receive(notification)
		listener.close()
	}

	delete(notifier.listeners, notification.TransactionID)
}

func (notifier *channelLevelNotifier) registerListener(done <-chan struct{}, transactionID string) <-chan Notification {
	notifyChannel := make(chan Notification, 1) // avoid blocking and only expect one notification per channel
	listener := &transactionListener{
		done:          done,
		transactionID: transactionID,
		notifyChannel: notifyChannel,
	}

	notifier.lock.Lock()
	notifier.listeners[transactionID] = append(notifier.listeners[transactionID], listener)
	notifier.lock.Unlock()

	return notifyChannel
}

func (notifier *channelLevelNotifier) close() {
	notifier.lock.Lock()
	defer notifier.lock.Unlock()

	for _, listeners := range notifier.listeners {
		for _, listener := range listeners {
			listener.close()
		}
	}

	notifier.listeners = nil
	notifier.closed = true
}

func (notifier *channelLevelNotifier) isClosed() bool {
	notifier.lock.Lock()
	defer notifier.lock.Unlock()

	return notifier.closed
}

type transactionListener struct {
	done          <-chan struct{}
	transactionID string
	notifyChannel chan<- Notification
}

func (listener *transactionListener) isDone() bool {
	select {
	case <-listener.done:
		return true
	default:
		return false
	}
}

func (listener *transactionListener) close() {
	close(listener.notifyChannel)
}

func (listener *transactionListener) receive(notification *Notification) {
	listener.notifyChannel <- *notification
}
