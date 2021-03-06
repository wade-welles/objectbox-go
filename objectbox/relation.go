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

type conditionRelationOneToMany struct {
	relation   *RelationToOne
	conditions []Condition
}

func (condition *conditionRelationOneToMany) applyTo(qb *QueryBuilder, isRoot bool) (ConditionId, error) {
	return 0, qb.LinkOneToMany(condition.relation, condition.conditions)
}

// RelationToOne holds information about a relation link on a property.
// It is used in generated entity code, providing a way to create a query across multiple related entities.
// Internally, the property value holds an ID of an object in the target entity.
type RelationToOne struct {
	Property *BaseProperty
	Target   *Entity
}

func (relation RelationToOne) entityId() TypeId {
	return relation.Property.Entity.Id
}

func (relation RelationToOne) propertyId() TypeId {
	return relation.Property.Id
}

func (relation *RelationToOne) Link(conditions ...Condition) Condition {
	return &conditionRelationOneToMany{relation, conditions}
}

func (relation RelationToOne) Equals(value uint64) Condition {
	return &conditionClosure{
		func(qb *QueryBuilder) (ConditionId, error) {
			return qb.IntEqual(relation.Property, int64(value))
		},
	}
}

func (relation RelationToOne) NotEquals(value uint64) Condition {
	return &conditionClosure{
		func(qb *QueryBuilder) (ConditionId, error) {
			return qb.IntNotEqual(relation.Property, int64(value))
		},
	}
}

func (relation RelationToOne) int64Slice(values []uint64) []int64 {
	result := make([]int64, len(values))

	for i, v := range values {
		result[i] = int64(v)
	}

	return result
}

func (relation RelationToOne) In(values ...uint64) Condition {
	return &conditionClosure{
		func(qb *QueryBuilder) (ConditionId, error) {
			return qb.Int64In(relation.Property, relation.int64Slice(values))
		},
	}
}

func (relation RelationToOne) NotIn(values ...uint64) Condition {
	return &conditionClosure{
		func(qb *QueryBuilder) (ConditionId, error) {
			return qb.Int64NotIn(relation.Property, relation.int64Slice(values))
		},
	}
}

type conditionRelationManyToMany struct {
	relation   *RelationToMany
	conditions []Condition
}

func (condition *conditionRelationManyToMany) applyTo(qb *QueryBuilder, isRoot bool) (ConditionId, error) {
	return 0, qb.LinkManyToMany(condition.relation, condition.conditions)
}

// RelationToMany holds information about a standalone relation link between two entities.
// It is used in generated entity code, providing a way to create a query across multiple related entities.
// Internally, the relation is stored separately, holding pairs of source & target object IDs.
type RelationToMany struct {
	Id     TypeId
	Source *Entity
	Target *Entity
}

func (relation *RelationToMany) Link(conditions ...Condition) Condition {
	return &conditionRelationManyToMany{relation, conditions}
}

// TODO contains() would make sense for many-to-many (slice)
