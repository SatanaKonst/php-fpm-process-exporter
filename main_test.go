package main

import "testing"

func TestClassifyProcessMaster(t *testing.T) {
	role, workerPool, masterConfig := classifyProcess("php-fpm: master process (/etc/php74w/php-fpm.gazprom.conf)")
	if role != "master" {
		t.Fatalf("role = %q, want %q", role, "master")
	}
	if workerPool != "" {
		t.Fatalf("workerPool = %q, want empty", workerPool)
	}
	if masterConfig != "/etc/php74w/php-fpm.gazprom.conf" {
		t.Fatalf("masterConfig = %q, want %q", masterConfig, "/etc/php74w/php-fpm.gazprom.conf")
	}
}

func TestClassifyProcessWorker(t *testing.T) {
	role, workerPool, masterConfig := classifyProcess("php-fpm: pool gazprom-php74w.conf")
	if role != "worker" {
		t.Fatalf("role = %q, want %q", role, "worker")
	}
	if workerPool != "gazprom-php74w.conf" {
		t.Fatalf("workerPool = %q, want %q", workerPool, "gazprom-php74w.conf")
	}
	if masterConfig != "" {
		t.Fatalf("masterConfig = %q, want empty", masterConfig)
	}
}

func TestEscapeLabelValue(t *testing.T) {
	got := escapeLabelValue(`a\b"c
d`)
	want := `a\\b\"c\nd`
	if got != want {
		t.Fatalf("escapeLabelValue = %q, want %q", got, want)
	}
}

