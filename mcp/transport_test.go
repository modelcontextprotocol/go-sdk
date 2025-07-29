// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestBatchFraming(t *testing.T) {
	// This test checks that the ndjsonFramer can read and write JSON batches.
	//
	// The framer is configured to write a batch size of 2, and we confirm that
	// nothing is sent over the wire until the second write, at which point both
	// messages become available.
	ctx := context.Background()

	r, w := io.Pipe()
	tport := newIOConn(rwc{r, w})
	tport.outgoingBatch = make([]jsonrpc.Message, 0, 2)

	// Read the two messages into a channel, for easy testing later.
	read := make(chan jsonrpc.Message)
	go func() {
		for range 2 {
			msg, _ := tport.Read(ctx)
			read <- msg
		}
	}()

	// The first write should not yet be observed by the reader.
	tport.Write(ctx, &jsonrpc.Request{ID: jsonrpc2.Int64ID(1), Method: "test"})
	select {
	case got := <-read:
		t.Fatalf("after one write, got message %v", got)
	default:
	}

	// ...but the second write causes both messages to be observed.
	tport.Write(ctx, &jsonrpc.Request{ID: jsonrpc2.Int64ID(2), Method: "test"})
	for _, want := range []int64{1, 2} {
		got := <-read
		if got := got.(*jsonrpc.Request).ID.Raw(); got != want {
			t.Errorf("got message #%d, want #%d", got, want)
		}
	}
}

func Test_ioConn_Read_BadTrailingData(t *testing.T) {
	type fields struct {
		rwc io.ReadWriteCloser
	}
	type args struct {
		ctx context.Context
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "bad data at the end of first valid json",
			fields: fields{
				rwc: rwc{
					rc: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"test","params":{}},`)),
				},
			},
			args: args{
				ctx: context.Background(),
			},
			want:    "invalid trailing data ',' at the end of stream",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newIOConn(tt.fields.rwc)
			_, err := tr.Read(tt.args.ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("ioConn.Read() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(err.Error(), tt.want) {
				t.Errorf("ioConn.Read() = %v, want %v", err.Error(), tt.want)
			}
		})
	}
}
