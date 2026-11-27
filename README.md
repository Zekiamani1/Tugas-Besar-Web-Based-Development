# Milestone 2 Tugas Besar IF3110 Web-Based Development

## Anggota Kelompok

- 13524058 Muhammad Naufal Romi Annafi
- 13524082 Zeki Amani
- 13524133 Muhammad Daffa Arrizki Yanma

## Deskripsi

Database Manager adalah platform web untuk mengelola akses dan perubahan pada database bersama. Platform Admin mengatur pengguna, group, leader, koneksi, dan izin database. Pengguna dapat menjelajahi database yang diizinkan serta mengajukan migration untuk ditinjau dan dijalankan secara asynchronous. Milestone 2 mewajibkan PostgreSQL sebagai database target; backend juga menyediakan dukungan database viewer untuk MySQL dan SQLite.

## Fitur

- Login menggunakan username dan password; akun pengguna dibuat oleh Platform Admin.
- Pengelolaan pengguna dan group, termasuk anggota dan leader group.
- Pemberian izin database kepada group dengan tingkat **View Only** atau **View + Edit**.
- Ringkasan database yang dapat diakses setiap group.
- Database viewer untuk melihat schema, tabel, kolom, constraint, index, dan data.
- Pembuatan draft migration dengan SQL up dan down, atau penandaan irreversible beserta alasannya. SQL yang pernah dieksekusi tidak dapat diubah.
- Approval oleh leader seluruh group yang memiliki akses ke database, termasuk group dengan akses View Only.
- Eksekusi migration secara asynchronous melalui RabbitMQ dan worker, termasuk mode batch, checkpoint, dan resume.
- Rollback dan forward sesuai urutan migration, serta riwayat attempt dan filter **Hide rollback**.
- Structured logging JSON dan tracing OpenTelemetry pada server dan worker; Collector, metrik, dan dashboard Grafana tersedia melalui Compose.
- Session dengan idle timeout, absolute expiration, rotasi ID, dan grace period.
- Perlindungan CSRF untuk form browser.

## Struktur Program

```text
.
├── backend/
│   ├── cmd/server/                      # Entry point, routing, dan API server
│   ├── cmd/worker/                      # Entry point worker migration
│   ├── internal/middleware/             # Auth, otorisasi, CORS, dan CSRF
│   ├── internal/models/                 # Model data
│   ├── internal/observability/          # Logging, tracing, dan metrik
│   ├── internal/service/                # Access control, database, dan migration
│   ├── Dockerfile                       # Build server dan worker
│   └── go.mod                           # Modul dan dependensi Go
├── frontend/                            # Aplikasi Next.js dan tes frontend
├── observability/                       # Konfigurasi Collector dan Grafana
├── docker-compose.yml                   # Layanan aplikasi dan dependensinya
├── .env.example                         # Contoh konfigurasi lingkungan
└── README.md
```

## Prasyarat

- Go 1.26.8 atau lebih baru.
- Docker Engine dan Docker Compose untuk menjalankan aplikasi serta integration test.
- Bun untuk pengembangan dan pengujian frontend di luar Docker.
- Browser web modern.

## Cara Setup dan Menjalankan

Jalankan perintah berikut dari direktori utama repository:

```bash
test -f .env || cp .env.example .env
```

Sebelum menjalankan aplikasi:

1. Ubah `ADMIN_PASSWORD`, password PostgreSQL, dan `RABBITMQ_PASSWORD` pada `.env`.
2. Pastikan password pada `DATABASE_URL` sama dengan `POSTGRES_PASSWORD`.
3. Pastikan kredensial pada `RABBITMQ_URL` sama dengan `RABBITMQ_USER` dan `RABBITMQ_PASSWORD`.
4. Untuk penggunaan lokal melalui HTTP, atur `SESSION_COOKIE_SECURE=false`. Gunakan `true` saat aplikasi diakses melalui HTTPS.

Jika `.env` sudah ada, sesuaikan isinya tanpa menimpanya. Sesuaikan juga `NEXT_PUBLIC_API_URL` dan `CORS_ALLOWED_ORIGINS` jika port atau alamat frontend/backend diubah.

Jalankan frontend, backend, worker, PostgreSQL internal, RabbitMQ, dan layanan observability:

```bash
docker compose up --build
```

Buka [http://localhost:3000](http://localhost:3000), lalu login menggunakan akun admin yang dikonfigurasi pada `.env`. API backend tersedia di [http://localhost:8080](http://localhost:8080). Port host dapat diubah melalui `FRONTEND_PORT` dan `APP_PORT`.

Lihat log server dan worker dengan `docker compose logs -f backend worker`. Grafana tersedia di [http://localhost:3001](http://localhost:3001), dengan dashboard **Database Platform**; konfigurasi default image menyediakan login `admin` / `admin`. Metrik berformat Prometheus tersedia di [http://localhost:9464/metrics](http://localhost:9464/metrics).

Jalankan tes Go dari direktori `backend` dengan `go test ./...`. Tes integrasi migration memerlukan PostgreSQL khusus pengujian melalui `MIGRATION_TEST_DSN`; tes worker tertentu juga memerlukan `RABBITMQ_TEST_URL`. Jangan gunakan database pengguna sebagai database tes. Untuk tes frontend, jalankan `bun install --frozen-lockfile` dan `bun run test` dari direktori `frontend`.

Saat mendaftarkan PostgreSQL atau MySQL melalui Admin Panel, gunakan host dan port yang dapat dijangkau oleh container aplikasi.

### Batasan pendaftaran SQLite

Untuk SQLite, isi nama database, tipe `sqlite`, dan path file pada field nama database. Path tersebut harus dapat diakses oleh container backend. Kewajiban dukungan database target untuk Milestone 2 adalah PostgreSQL; ketersediaan fitur migration pada DBMS lain tidak dijamin.

Untuk menghentikan aplikasi, tekan `Ctrl+C`. Data PostgreSQL internal disimpan dalam Docker volume.
