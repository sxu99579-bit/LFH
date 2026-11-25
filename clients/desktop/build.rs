fn main() {
    #[cfg(windows)]
    {
        let mut res = winres::WindowsResource::new();
        res.set_icon("assets/quailnest.ico");
        res.set("ProductName", "QuailNest Desktop");
        res.set("FileDescription", "QuailNest 鹌鹑窝 桌面客户端");
        if let Err(err) = res.compile() {
            println!("cargo:warning=winres compile failed: {err}");
        }
    }
}
