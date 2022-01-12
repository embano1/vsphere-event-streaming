package main

import (
	"testing"

	"github.com/embano1/memlog"
)

func Test_getStart(t *testing.T) {
	type args struct {
		earliest memlog.Offset
		latest   memlog.Offset
		pageSize int
	}
	tests := []struct {
		name string
		args args
		want memlog.Offset
	}{
		{
			name: "empty log",
			args: args{
				earliest: -1,
				latest:   -1,
				pageSize: 50,
			},
			want: -1,
		},
		{
			name: "earliest 0, latest 10, page 50",
			args: args{
				earliest: 0,
				latest:   10,
				pageSize: 50,
			},
			want: 0,
		},
		{
			name: "earliest 0, latest 100, page 50",
			args: args{
				earliest: 0,
				latest:   100,
				pageSize: 50,
			},
			want: 51,
		},
		{
			name: "earliest 99, latest 100, page 50",
			args: args{
				earliest: 99,
				latest:   100,
				pageSize: 50,
			},
			want: 99,
		},
		{
			name: "earliest 99, latest 100, page 50",
			args: args{
				earliest: 99,
				latest:   100,
				pageSize: 50,
			},
			want: 99,
		},
		{
			name: "earliest 51, latest 89, page 50",
			args: args{
				earliest: 51,
				latest:   89,
				pageSize: 50,
			},
			want: 51,
		},
		{
			name: "earliest 151, latest 304, page 50",
			args: args{
				earliest: 151,
				latest:   304,
				pageSize: 50,
			},
			want: 255,
		},
		{
			name: "earliest 151, latest 304, page 10",
			args: args{
				earliest: 151,
				latest:   304,
				pageSize: 10,
			},
			want: 295,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getStart(tt.args.earliest, tt.args.latest, tt.args.pageSize); got != tt.want {
				t.Errorf("getStart() = %v, want %v", got, tt.want)
			}
		})
	}
}
