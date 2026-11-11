// Copyright (c) 2026 Jonas Kaninda.
//
// This file is part of Miabi.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package dockerclient

import (
	"testing"

	"github.com/miabi-io/miabi/pkg/stack/docker"
)

// The stack asks for IPv6 explicitly; dropping the flag left `miabi setup --ipv6` creating IPv4-only networks.
func TestCreateNetworkOptionsCarriesIPv6(t *testing.T) {
	on := true
	opts, err := createNetworkOptions(docker.NetworkSpec{
		Name: "miabi", Subnet: "10.63.0.0/16", EnableIPv6: &on, IPv6Subnet: "fd42:6d69:6162:1::/64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.EnableIPv6 == nil || !*opts.EnableIPv6 {
		t.Fatal("EnableIPv6 not set on the create options")
	}
	if opts.IPAM == nil || len(opts.IPAM.Config) != 2 || opts.IPAM.Config[1].Subnet.String() != "fd42:6d69:6162:1::/64" {
		t.Fatalf("IPAM = %+v, want the IPv4 and IPv6 subnets", opts.IPAM)
	}

	if _, err := createNetworkOptions(docker.NetworkSpec{Name: "x", IPv6Subnet: "nope"}); err == nil {
		t.Fatal("an invalid IPv6 subnet was accepted")
	}
}
