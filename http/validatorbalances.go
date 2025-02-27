// Copyright © 2020, 2021 Attestant Limited.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package http

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	api "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/pkg/errors"
)

type validatorBalancesJSON struct {
	ExecutionOptimistic bool                    `json:"execution_optimistic"`
	Finalized           bool                    `json:"finalized"`
	Data                []*api.ValidatorBalance `json:"data"`
}

// ValidatorBalances provides the validator balances for a given state.
// stateID can be a slot number or state root, or one of the special values "genesis", "head", "justified" or "finalized".
// validatorIndices is a list of validator indices to restrict the returned values.  If no validators are supplied no filter
// will be applied.

type validatorBalancesResponse = api.Response[map[phase0.ValidatorIndex]phase0.Gwei]

func (s *Service) ValidatorBalances(ctx context.Context, stateID string, validatorIndices []phase0.ValidatorIndex) (*validatorBalancesResponse, error) {
	if stateID == "" {
		return nil, errors.New("no state ID specified")
	}

	if len(validatorIndices) > s.indexChunkSize(ctx) {
		return s.chunkedValidatorBalances(ctx, stateID, validatorIndices)
	}

	url := fmt.Sprintf("/eth/v1/beacon/states/%s/validator_balances", stateID)
	if len(validatorIndices) != 0 {
		ids := make([]string, len(validatorIndices))
		for i := range validatorIndices {
			ids[i] = fmt.Sprintf("%d", validatorIndices[i])
		}
		url = fmt.Sprintf("%s?id=%s", url, strings.Join(ids, ","))
	}

	respBodyReader, err := s.get(ctx, url)
	if err != nil {
		return nil, errors.Wrap(err, "failed to request validator balances")
	}
	if respBodyReader == nil {
		return nil, errors.New("failed to obtain validator balances")
	}

	var validatorBalancesJSON validatorBalancesJSON
	if err := json.NewDecoder(respBodyReader).Decode(&validatorBalancesJSON); err != nil {
		return nil, errors.Wrap(err, "failed to parse validator balances")
	}
	if validatorBalancesJSON.Data == nil {
		return nil, errors.New("no validator balances returned")
	}

	res := &validatorBalancesResponse{
		Data: make(map[phase0.ValidatorIndex]phase0.Gwei),
		Metadata: map[string]interface{}{
			api.MetadataKeyExecutionOptimistic: validatorBalancesJSON.ExecutionOptimistic,
			api.MetadataKeyFinalized:           validatorBalancesJSON.Finalized,
		},
	}

	for _, validatorBalance := range validatorBalancesJSON.Data {
		res.Data[validatorBalance.Index] = validatorBalance.Balance
	}
	return res, nil
}

// chunkedValidatorBalances obtains the validator balances a chunk at a time.
func (s *Service) chunkedValidatorBalances(ctx context.Context, stateID string, validatorIndices []phase0.ValidatorIndex) (*validatorBalancesResponse, error) {
	res := &validatorBalancesResponse{
		Data: make(map[phase0.ValidatorIndex]phase0.Gwei),
	}
	indexChunkSize := s.indexChunkSize(ctx)
	for i := 0; i < len(validatorIndices); i += indexChunkSize {
		chunkStart := i
		chunkEnd := i + indexChunkSize
		if len(validatorIndices) < chunkEnd {
			chunkEnd = len(validatorIndices)
		}
		chunk := validatorIndices[chunkStart:chunkEnd]
		chunkRes, err := s.ValidatorBalances(ctx, stateID, chunk)
		if err != nil {
			return nil, errors.Wrap(err, "failed to obtain chunk")
		}
		for k, v := range chunkRes.Data {
			res.Data[k] = v
		}

		// For metadata, if any of the chunks are optimistic then the result is optimistic.
		// If any of the chunks are not finalized then the result is not finalized.
		finalized, err := chunkRes.Finalized()
		if err != nil {
			return nil, errors.Wrap(err, "failed to obtain finalized")
		}

		if !finalized {
			res.Metadata[api.MetadataKeyFinalized] = finalized
		}

		executionOptimistic, err := chunkRes.ExecutionOptimistic()
		if err != nil {
			return nil, errors.Wrap(err, "failed to obtain execution optimistic")
		}

		if executionOptimistic {
			res.Metadata[api.MetadataKeyExecutionOptimistic] = executionOptimistic
		}
	}
	return res, nil
}
