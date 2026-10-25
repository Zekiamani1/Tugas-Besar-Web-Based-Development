# Milestone 1 Tugas Besar IF3110 Web-Based Development

## Anggota Kelompok

- 13524058 Muhammad Naufal Romi Annafi
- 13524082 Zeki Amani
- 13524133 Muhammad Daffa Arrizki Yanma

## Deskripsi

Database Manager adalah aplikasi web untuk mengelola akses ke database target bersama (mendukung PostgreSQL, MySQL, dan SQLite). Platform Admin dapat mengatur pengguna, group, koneksi database, dan izin group. Pengguna dapat menjelajahi database yang diizinkan serta membuat, meninjau, dan menjalankan migration.

## Fitur

- Login menggunakan username dan password; akun pengguna dibuat oleh Platform Admin.
- Pengelolaan pengguna dan group, termasuk melihat anggota setiap group.
- Pemberian izin database kepada group dengan tingkat **View Only** atau **View + Edit**.
- Ringkasan database yang dapat diakses setiap group.
- Database viewer untuk melihat schema, tabel, kolom, constraint, index, dan data.
- Pembuatan dan pengubahan draft migration sebelum dieksekusi.
- Eksekusi migration dalam transaksi pada database target, dengan rollback jika SQL gagal.
- Riwayat migration yang mencatat versi, SQL, pelaksana, waktu, dan status.
- Session dengan idle timeout, absolute expiration, rotasi ID, dan grace period.
- Perlindungan CSRF untuk form browser.

## Struktur Program

```text
.
├── app.go                               # Routing, handler, dan integrasi service
├── main.go                              # Konfigurasi dan entry point server
├── internal/
│   ├── middleware/                      # Auth, otorisasi, dan CSRF
│   ├── models/                          # Model data dan ringkasan untuk UI
│   └── service/
│       ├── accesscontrol/               # User, session, group, dan izin
│       ├── dbmanagement/                # Koneksi dan database viewer
│       ├── interfaces/                  # Kontrak antarservice
│       └── migration/                   # Draft, eksekusi, dan riwayat migration
├── web/
│   ├── static/                          # CSS dan halaman login
│   └── templates/                       # Template halaman aplikasi
├── *test.go                             # Unit, integration, dan E2E test
├── docker-compose.yml                   # App dan PostgreSQL internal
├── Dockerfile
├── .env.example                         # Contoh konfigurasi lingkungan
├── go.mod                               # Modul dan dependensi Go
└── README.md
```

## Prasyarat

- Go 1.26.8 atau lebih baru.
- Docker Engine dan Docker Compose untuk menjalankan aplikasi serta integration test.
- Browser web modern.

## Cara Setup dan Menjalankan

Jalankan perintah berikut dari direktori utama repository:

```bash
cp .env.example .env
```

Sebelum menjalankan aplikasi:

1. Ubah `ADMIN_PASSWORD` dan password PostgreSQL pada `.env`.
2. Pastikan password pada `DATABASE_URL` sama dengan `POSTGRES_PASSWORD`.
3. Untuk penggunaan lokal melalui HTTP, atur `SESSION_COOKIE_SECURE=false`. Gunakan `true` saat aplikasi diakses melalui HTTPS.

Jalankan aplikasi dan PostgreSQL internal:

```bash
docker compose up --build
```

Buka [http://localhost:8080](http://localhost:8080), lalu login menggunakan `ADMIN_USERNAME` dan `ADMIN_PASSWORD` dari `.env`. Port host dapat diubah melalui `APP_PORT`.

Saat mendaftarkan database target melalui Admin Panel, pilih tipe database. Untuk PostgreSQL dan MySQL, gunakan host dan port yang dapat dijangkau oleh container aplikasi. Untuk SQLite, isi path file database pada field `db_name` (host, port, user, dan password dapat dikosongkan). Untuk menghentikan aplikasi, tekan `Ctrl+C`. Data PostgreSQL internal disimpan dalam Docker volume.