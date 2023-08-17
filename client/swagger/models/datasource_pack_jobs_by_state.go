// Code generated by go-swagger; DO NOT EDIT.

package models

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"context"

	"github.com/go-openapi/errors"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag"
)

// DatasourcePackJobsByState datasource pack jobs by state
//
// swagger:model datasource.PackJobsByState
type DatasourcePackJobsByState struct {

	// number of pack jobs in this state
	Count int64 `json:"count,omitempty"`

	// the state of the pack jobs
	State struct {
		ModelWorkState
	} `json:"state,omitempty"`
}

// Validate validates this datasource pack jobs by state
func (m *DatasourcePackJobsByState) Validate(formats strfmt.Registry) error {
	var res []error

	if err := m.validateState(formats); err != nil {
		res = append(res, err)
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

func (m *DatasourcePackJobsByState) validateState(formats strfmt.Registry) error {
	if swag.IsZero(m.State) { // not required
		return nil
	}

	return nil
}

// ContextValidate validate this datasource pack jobs by state based on the context it is used
func (m *DatasourcePackJobsByState) ContextValidate(ctx context.Context, formats strfmt.Registry) error {
	var res []error

	if err := m.contextValidateState(ctx, formats); err != nil {
		res = append(res, err)
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

func (m *DatasourcePackJobsByState) contextValidateState(ctx context.Context, formats strfmt.Registry) error {

	return nil
}

// MarshalBinary interface implementation
func (m *DatasourcePackJobsByState) MarshalBinary() ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return swag.WriteJSON(m)
}

// UnmarshalBinary interface implementation
func (m *DatasourcePackJobsByState) UnmarshalBinary(b []byte) error {
	var res DatasourcePackJobsByState
	if err := swag.ReadJSON(b, &res); err != nil {
		return err
	}
	*m = res
	return nil
}
