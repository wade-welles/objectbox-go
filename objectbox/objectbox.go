/*
 * Copyright 2019 ObjectBox Ltd. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package objectbox

/*
#cgo LDFLAGS: -lobjectbox
#include <stdlib.h>
#include "objectbox.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"sync"

	"github.com/google/flatbuffers/go"
)

//noinspection GoUnusedConst
const (
	DebugFlags_LOG_TRANSACTIONS_READ  = 1
	DebugFlags_LOG_TRANSACTIONS_WRITE = 2
	DebugFlags_LOG_QUERIES            = 4
	DebugFlags_LOG_QUERY_PARAMETERS   = 8
	DebugFlags_LOG_ASYNC_QUEUE        = 16
)

// atomic boolean true & false
const aTrue = 1
const aFalse = 0

type TypeId uint32

type ObjectBox struct {
	store          *C.OBX_store
	entitiesById   map[TypeId]*entity
	entitiesByName map[string]*entity
	boxes          map[TypeId]*Box
	boxesMutex     *sync.Mutex
	options        options
}

type options struct {
	putAsyncTimeout  uint
	alwaysAwaitAsync bool
}

type txnFun func(transaction *Transaction) error
type cursorFun func(cursor *Cursor) error

// constant during runtime so no need to call this each time it's necessary
var supportsBytesArray = bool(C.obx_supports_bytes_array())

// Close fully closes the database and free's resources
func (ob *ObjectBox) Close() {
	storeToClose := ob.store
	ob.store = nil
	if storeToClose != nil {
		C.obx_store_close(storeToClose)
	}

	ob.boxesMutex.Lock()
	defer ob.boxesMutex.Unlock()
	for _, box := range ob.boxes {
		if err := box.close(); err != nil {
			fmt.Println(err)
		}
	}
	ob.boxes = nil
}

func (ob *ObjectBox) beginTxn() (*Transaction, error) {
	var ctxn = C.obx_txn_begin(ob.store)
	if ctxn == nil {
		return nil, createError()
	}
	return &Transaction{ctxn, ob}, nil
}

func (ob *ObjectBox) beginTxnRead() (*Transaction, error) {
	var ctxn = C.obx_txn_begin_read(ob.store)
	if ctxn == nil {
		return nil, createError()
	}
	return &Transaction{ctxn, ob}, nil
}

func (ob *ObjectBox) runInTxn(readOnly bool, txnFun txnFun) (err error) {
	runtime.LockOSThread()
	var txn *Transaction
	if readOnly {
		txn, err = ob.beginTxnRead()
	} else {
		txn, err = ob.beginTxn()
	}
	if err != nil {
		runtime.UnlockOSThread()
		return
	}

	//fmt.Println(">>> START TX")
	//os.Stdout.Sync()

	// Defer to ensure a TX is ALWAYS closed, even in a panic
	defer func() {
		err2 := txn.Close()
		if err == nil {
			err = err2
		}
		runtime.UnlockOSThread()
	}()

	err = txnFun(txn)

	//fmt.Println("<<< END TX")
	//os.Stdout.Sync()

	if !readOnly && err == nil {
		err = txn.Commit()
	}

	//fmt.Println("<<< END TX Close")
	//os.Stdout.Sync()

	return
}

func (ob ObjectBox) getEntityById(typeId TypeId) *entity {
	entity := ob.entitiesById[typeId]
	if entity == nil {
		// Configuration error by the dev, OK to panic
		panic("Configuration error; no entity registered for type ID " + strconv.Itoa(int(typeId)))
	}
	return entity
}

func (ob ObjectBox) getEntityByName(typeName string) *entity {
	entity := ob.entitiesByName[typeName]
	if entity == nil {
		// Configuration error by the dev, OK to panic
		panic("Configuration error; no entity registered for type name " + typeName)
	}
	return entity
}

func (ob *ObjectBox) runWithCursor(e *entity, readOnly bool, cursorFun cursorFun) error {
	if ob.options.alwaysAwaitAsync {
		e.awaitAsyncCompletion()
	}

	return ob.runInTxn(readOnly, func(txn *Transaction) error {
		return txn.runWithCursor(e, cursorFun)
	})
}

// SetDebugFlags configures debug logging of the ObjectBox core.
// See DebugFlags_* constants
func (ob *ObjectBox) SetDebugFlags(flags uint) error {
	rc := C.obx_store_debug_flags(ob.store, C.OBXDebugFlags(flags))
	if rc != 0 {
		return createError()
	}
	return nil
}

// panics on error (in case entity with the given ID doesn't exist)
func (ob *ObjectBox) InternalBox(typeId TypeId) *Box {
	box, err := ob.box(typeId)
	if err != nil {
		panic(fmt.Sprintf("Could not create box for type ID %d: %s", typeId, err))
	}
	return box
}

// Gets an Entity Box which provides CRUD access to objects of the given type
func (ob *ObjectBox) box(typeId TypeId) (*Box, error) {
	ob.boxesMutex.Lock()
	defer ob.boxesMutex.Unlock()

	box := ob.boxes[typeId]
	if box != nil {
		return box, nil
	}

	entity := ob.getEntityById(typeId)
	cbox := C.obx_box_create(ob.store, C.obx_schema_id(typeId))
	if cbox == nil {
		return nil, createError()
	}

	box = &Box{
		objectBox: ob,
		box:       cbox,
		fbb:       flatbuffers.NewBuilder(512),
		entity:    entity,
	}
	ob.boxes[typeId] = box
	return box, nil
}

// AwaitAsyncCompletion blocks until all PutAsync insert have been processed
func (ob *ObjectBox) AwaitAsyncCompletion() *ObjectBox {
	if C.obx_store_await_async_completion(ob.store) != 0 {
		fmt.Println(createError())
	}
	return ob
}

func createError() error {
	msg := C.obx_last_error_message()
	if msg == nil {
		return errors.New("no error info available; please report")
	} else {
		return errors.New(C.GoString(msg))
	}
}
