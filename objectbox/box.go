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
	"reflect"
	"sync/atomic"
	"unsafe"

	"github.com/google/flatbuffers/go"
)

// Box provides CRUD access to objects of a common type
type Box struct {
	objectBox *ObjectBox
	box       *C.OBX_box
	entity    *entity

	// Must be used in combination with fbbInUseAtomic
	fbb *flatbuffers.Builder

	// Values 0 (fbb available) or 1 (fbb in use); use only with CompareAndSwapInt32
	fbbInUseAtomic uint32
}

// close fully closes the Box connection and free's resources
func (box *Box) close() (err error) {
	rc := C.obx_box_close(box.box)
	box.box = nil
	if rc != 0 {
		err = createError()
	}
	return
}

// Creates a query with the given conditions. Use generated properties to create conditions.
// Keep the Query object if you intend to execute it multiple times.
// Note: this function panics if you try to create illegal queries; e.g. use properties of an alien type.
// This is typically a programming error. Use QueryOrError instead if you want the explicit error check.
func (box *Box) Query(conditions ...Condition) *Query {
	query, err := box.QueryOrError(conditions...)
	if err != nil {
		panic(fmt.Sprintf("Could not create query - please check your query conditions: %s", err))
	}
	return query
}

// Like Query() but with error handling; e.g. when you build conditions dynamically that may fail.
func (box *Box) QueryOrError(conditions ...Condition) (query *Query, err error) {
	builder := newQueryBuilder(box.objectBox, box.entity.id)

	defer func() {
		err2 := builder.Close()
		if err == nil && err2 != nil {
			err = err2
			query = nil
		}
	}()

	if err = builder.applyConditions(conditions); err != nil {
		return nil, err
	}

	query, err = builder.Build()

	return // NOTE result might be overwritten by the deferred "closer" function
}

func (box *Box) idForPut(idCandidate uint64) (id uint64, err error) {
	id = uint64(C.obx_box_id_for_put(box.box, C.obx_id(idCandidate)))
	if id == 0 {
		err = createError()
	}
	return
}

// PutAsync asynchronously inserts/updates a single object.
// When inserting, the ID property on the passed object will be assigned the new ID as well.
//
// It's executed on a separate internal thread for better performance.
//
// There are two main use cases:
//
// 1) "Put & Forget:" you gain faster puts as you don't have to wait for the transaction to finish.
//
// 2) Many small transactions: if your write load is typically a lot of individual puts that happen in parallel,
// this will merge small transactions into bigger ones. This results in a significant gain in overall throughput.
//
//
// In situations with (extremely) high async load, this method may be throttled (~1ms) or delayed
// up to options.putAsyncTimeout (10 seconds by default). In the unlikely event that the object could
// not be enqueued after delaying (because of a full queue), an error will be returned.
//
// Note that this method does not give you hard durability guarantees like the synchronous Put provides.
// There is a small time window in which the data may not have been committed durably yet.
func (box *Box) PutAsync(object interface{}) (id uint64, err error) {
	return box.PutAsyncWithTimeout(object, box.objectBox.options.putAsyncTimeout)
}

// Same as PutAsync but with a custom enqueue timeout
func (box *Box) PutAsyncWithTimeout(object interface{}, timeoutMs uint) (id uint64, err error) {
	idFromObject, err := box.entity.binding.GetId(object)
	if err != nil {
		return 0, err
	}

	if box.entity.hasRelations {
		return 0, errors.New("PutAsync is currently not supported on entities that have relations")
	}

	if id, err = box.idForPut(idFromObject); err != nil {
		return 0, err
	}

	// TODO put related entities and this one within a single transaction
	//if box.entity.hasRelations {
	//	err = box.objectBox.runInTxn(false, func(txn *Transaction) error {
	//		return box.entity.binding.PutRelated(txn, object, id)
	//	})
	//	if err != nil {
	//		return 0, err
	//	}
	//}

	var fbb *flatbuffers.Builder
	if atomic.CompareAndSwapUint32(&box.fbbInUseAtomic, 0, 1) {
		defer atomic.StoreUint32(&box.fbbInUseAtomic, 0)
		fbb = box.fbb
	} else {
		fbb = flatbuffers.NewBuilder(256)
	}

	if err = box.entity.binding.Flatten(object, fbb, id); err != nil {
		return 0, err
	}

	checkForPreviousValue := idFromObject != 0
	if err = box.finishFbbAndPutAsync(fbb, id, checkForPreviousValue, timeoutMs); err != nil {
		return 0, err
	}

	// update the id on the object
	if idFromObject != id {
		box.entity.binding.SetId(object, id)
	}

	return id, nil
}

func (box *Box) finishFbbAndPutAsync(fbb *flatbuffers.Builder, id uint64, checkForPreviousObject bool, timeoutMs uint) (err error) {
	fbb.Finish(fbb.EndObject())
	bytes := fbb.FinishedBytes()

	box.entity.markOutOfSync()

	rc := C.obx_box_put_async(box.box,
		C.obx_id(id), unsafe.Pointer(&bytes[0]), C.size_t(len(bytes)), C.bool(checkForPreviousObject), C.uint64_t(timeoutMs))
	if rc != 0 {
		err = createError()
	}

	// Reset to have a clear state for the next caller
	fbb.Reset()

	return
}

// Put synchronously inserts/updates a single object.
// In case the ID is not specified, it would be assigned automatically (auto-increment).
// When inserting, the ID property on the passed object will be assigned the new ID as well.
func (box *Box) Put(object interface{}) (id uint64, err error) {
	err = box.objectBox.runWithCursor(box.entity, false, func(cursor *Cursor) error {
		var errInner error
		id, errInner = cursor.Put(object)
		return errInner
	})
	return
}

// PutAll inserts multiple objects in single transaction.
// The given argument must be a slice of the object type this Box represents (pointers to objects).
// In case IDs are not set on the objects, they would be assigned automatically (auto-increment).
//
// Returns: IDs of the put objects (in the same order).
//
// Note: In case an error occurs during the transaction, some of the objects may already have the ID assigned
// even though the transaction has been rolled back and the objects are not stored under those IDs.
//
// Note: The slice may be empty or even nil; in both cases, an empty IDs slice and no error is returned.
func (box *Box) PutAll(slice interface{}) (ids []uint64, err error) {
	if slice == nil {
		return []uint64{}, nil
	}

	sliceValue := reflect.ValueOf(slice)
	count := sliceValue.Len()
	if count == 0 {
		return []uint64{}, nil
	}
	err = box.objectBox.runWithCursor(box.entity, false, func(cursor *Cursor) error {
		ids = make([]uint64, count)
		for i := 0; i < count; i++ {
			id, errPut := cursor.Put(sliceValue.Index(i).Interface())
			if errPut != nil {
				// Note that objects that have been put before already have an ID assigned; similar to when an TX fails
				return errPut
			}
			ids[i] = id
		}
		return nil
	})
	return
}

// Remove deletes a single object
func (box *Box) Remove(id uint64) (err error) {
	return box.objectBox.runWithCursor(box.entity, false, func(cursor *Cursor) error {
		return cursor.Remove(id)
	})
}

// RemoveAll removes all stored objects.
// This is much faster than removing objects one by one in a loop.
func (box *Box) RemoveAll() (err error) {
	return box.objectBox.runWithCursor(box.entity, false, func(cursor *Cursor) error {
		return cursor.RemoveAll()
	})
}

// Count returns a number of objects stored
func (box *Box) Count() (count uint64, err error) {
	err = box.objectBox.runWithCursor(box.entity, true, func(cursor *Cursor) error {
		var errInner error
		count, errInner = cursor.Count()
		return errInner
	})
	return
}

// CountMax returns a number of objects stored (up to a given maximum)
func (box *Box) CountMax(max uint64) (count uint64, err error) {
	err = box.objectBox.runWithCursor(box.entity, true, func(cursor *Cursor) error {
		var errInner error
		count, errInner = cursor.CountMax(max)
		return errInner
	})
	return
}

// IsEmpty checks whether the box contains any objects
func (box *Box) IsEmpty() (result bool, err error) {
	err = box.objectBox.runWithCursor(box.entity, true, func(cursor *Cursor) error {
		var errInner error
		result, errInner = cursor.IsEmpty()
		return errInner
	})
	return
}

// Get reads a single object.
//
// Returns an interface that should be cast to the appropriate type.
// Returns nil in case the object with the given ID doesn't exist.
// The cast is done automatically when using the generated BoxFor* code
func (box *Box) Get(id uint64) (object interface{}, err error) {
	err = box.objectBox.runWithCursor(box.entity, true, func(cursor *Cursor) error {
		var errInner error
		object, errInner = cursor.Get(id)
		return errInner
	})
	return
}

// Get reads a all stored objects
//
// Returns a slice of objects that should be cast to the appropriate type.
// The cast is done automatically when using the generated BoxFor* code
func (box *Box) GetAll() (slice interface{}, err error) {
	err = box.objectBox.runWithCursor(box.entity, true, func(cursor *Cursor) error {
		var errInner error
		slice, errInner = cursor.GetAll()
		return errInner
	})
	return
}

// Contains checks whether an object with the given ID is stored.
func (box *Box) Contains(id uint64) (bool, error) {
	var found = false
	var err = box.objectBox.runWithCursor(box.entity, true, func(cursor *Cursor) error {
		var errInner error
		found, errInner = cursor.seek(id)
		return errInner
	})
	return found, err
}
