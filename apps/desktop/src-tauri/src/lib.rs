// SliilS desktop entrypoint.
//
// M0: minimal scaffold. Wired up properly once apps/web ships routes worth
// showing natively (M1+).

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .run(tauri::generate_context!())
        .expect("error while running SliilS desktop");
}

#[cfg(test)]
mod tests {
    #[test]
    fn smoke() {
        assert_eq!(2 + 2, 4);
    }
}
