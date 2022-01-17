// Code generated by MockGen. DO NOT EDIT.
// Source: pkg/application/application.go

// Package application is a generated GoMock package.
package application

import (
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"
	component "github.com/redhat-developer/odo/pkg/component"
)

// MockClient is a mock of Client interface.
type MockClient struct {
	ctrl     *gomock.Controller
	recorder *MockClientMockRecorder
}

// MockClientMockRecorder is the mock recorder for MockClient.
type MockClientMockRecorder struct {
	mock *MockClient
}

// NewMockClient creates a new mock instance.
func NewMockClient(ctrl *gomock.Controller) *MockClient {
	mock := &MockClient{ctrl: ctrl}
	mock.recorder = &MockClientMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockClient) EXPECT() *MockClientMockRecorder {
	return m.recorder
}

// ComponentList mocks base method.
func (m *MockClient) ComponentList(name string) ([]component.Component, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ComponentList", name)
	ret0, _ := ret[0].([]component.Component)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ComponentList indicates an expected call of ComponentList.
func (mr *MockClientMockRecorder) ComponentList(name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ComponentList", reflect.TypeOf((*MockClient)(nil).ComponentList), name)
}

// Delete mocks base method.
func (m *MockClient) Delete(name string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Delete", name)
	ret0, _ := ret[0].(error)
	return ret0
}

// Delete indicates an expected call of Delete.
func (mr *MockClientMockRecorder) Delete(name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Delete", reflect.TypeOf((*MockClient)(nil).Delete), name)
}

// Exists mocks base method.
func (m *MockClient) Exists(app string) (bool, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Exists", app)
	ret0, _ := ret[0].(bool)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Exists indicates an expected call of Exists.
func (mr *MockClientMockRecorder) Exists(app interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Exists", reflect.TypeOf((*MockClient)(nil).Exists), app)
}

// GetMachineReadableFormat mocks base method.
func (m *MockClient) GetMachineReadableFormat(appName, projectName string) App {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetMachineReadableFormat", appName, projectName)
	ret0, _ := ret[0].(App)
	return ret0
}

// GetMachineReadableFormat indicates an expected call of GetMachineReadableFormat.
func (mr *MockClientMockRecorder) GetMachineReadableFormat(appName, projectName interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetMachineReadableFormat", reflect.TypeOf((*MockClient)(nil).GetMachineReadableFormat), appName, projectName)
}

// GetMachineReadableFormatForList mocks base method.
func (m *MockClient) GetMachineReadableFormatForList(apps []App) AppList {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetMachineReadableFormatForList", apps)
	ret0, _ := ret[0].(AppList)
	return ret0
}

// GetMachineReadableFormatForList indicates an expected call of GetMachineReadableFormatForList.
func (mr *MockClientMockRecorder) GetMachineReadableFormatForList(apps interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetMachineReadableFormatForList", reflect.TypeOf((*MockClient)(nil).GetMachineReadableFormatForList), apps)
}

// List mocks base method.
func (m *MockClient) List() ([]string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "List")
	ret0, _ := ret[0].([]string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// List indicates an expected call of List.
func (mr *MockClientMockRecorder) List() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "List", reflect.TypeOf((*MockClient)(nil).List))
}