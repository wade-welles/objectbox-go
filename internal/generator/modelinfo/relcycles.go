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

package modelinfo

import "fmt"

func (model *ModelInfo) CheckRelationCycles() error {
	// DFS cycle check, storing relation path in the recursion stack
	var visited = make(map[*Entity]bool)
	var recursionStack = make(map[*Entity]string)

	// call the recursive check starting in each entity
	for _, entity := range model.Entities {
		if err := entity.checkRelationCycles(&visited, &recursionStack); err != nil {
			return err
		}
	}

	return nil
}

func (entity *Entity) checkRelationCycles(visited *map[*Entity]bool, recursionStack *map[*Entity]string) error {
	if !(*visited)[entity] {
		(*visited)[entity] = true

		for _, rel := range entity.Relations {
			// overwrite this for each relation
			(*recursionStack)[entity] = rel.Name

			// this happens if the entity containing this relation haven't been defined in this file
			if rel.Target == nil {
				continue
			}

			if !(*visited)[rel.Target] {
				if err := rel.Target.checkRelationCycles(visited, recursionStack); err != nil {
					return err
				}
			}

			if (*recursionStack)[rel.Target] != "" {
				var cycle []string
				for ent, name := range *recursionStack {
					if name != "" {
						cycle = append(cycle, ent.Name+"."+name)
					}
				}
				return fmt.Errorf("relation cycle detected: %v", cycle)
			}
		}
	}

	delete(*recursionStack, entity)
	return nil

}
