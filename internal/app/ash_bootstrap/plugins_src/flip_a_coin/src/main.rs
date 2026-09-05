use std::env;
use std::fs::{File, OpenOptions};
use std::io::{Read, Write};
use std::path::Path;
use std::time::SystemTime;

const VERSION: &str = "1.0.0";
const PLUGIN_NAME: &str = "flip_a_coin";

fn get_ai_docs() -> String {
    r#"{
  "Capabilities": [
    "Simulate flipping one or more fair coins.",
    "Return random outcomes (HEADS or TAILS) with count statistics."
  ],
  "Arguments": {
    "--count": "Number of coins to flip (positive integer, default: 1)",
    "--format": "Output format: 'json' (default) or 'text'",
    "--ai-docs": "Print AI documentation and exit",
    "--version": "Print version and exit",
    "--help": "Print help and exit"
  },
  "Return format": {
    "status": "success or error",
    "result": "HEADS or TAILS when count=1",
    "flips": "array of flip outcomes",
    "heads_count": "number of HEADS",
    "tails_count": "number of TAILS"
  },
  "Usage guidance for the AI": "A single call to flip_a_coin performs the coin toss. Do not repeat the call. Directly report the result from the JSON response to the user."
}"#.to_string()
}

fn log_event(level: &str, msg: &str, fields: &[(&str, &str)]) {
    let log_file = match env::var("ASH_LOG_FILE") {
        Ok(val) if !val.trim().is_empty() => val,
        _ => return,
    };
    let verbose = env::var("ASH_VERBOSE").unwrap_or_default();
    let is_verbose = matches!(verbose.to_lowercase().as_str(), "1" | "true" | "yes" | "debug");

    if level == "DEBUG" && !is_verbose {
        return;
    }

    let format = env::var("ASH_LOG_FORMAT").unwrap_or_else(|_| "json".to_string());
    let path = Path::new(&log_file);
    if let Some(parent) = path.parent() {
        let _ = std::fs::create_dir_all(parent);
    }

    if let Ok(mut file) = OpenOptions::new().create(true).append(true).open(path) {
        let now = SystemTime::now()
            .duration_since(SystemTime::UNIX_EPOCH)
            .map(|d| d.as_secs())
            .unwrap_or(0);

        if format.to_lowercase() == "json" {
            let mut json_fields = String::new();
            for (k, v) in fields {
                json_fields.push_str(&format!(",\"{}\":\"{}\"", k, v.replace('"', "\\\"")));
            }
            let _ = writeln!(
                file,
                "{{\"time\":{},\"level\":\"{}\",\"plugin\":\"{}\",\"message\":\"{}\"{}}}",
                now, level.to_lowercase(), PLUGIN_NAME, msg.replace('"', "\\\""), json_fields
            );
        } else {
            let mut txt_fields = String::new();
            for (k, v) in fields {
                txt_fields.push_str(&format!(" {}={}", k, v));
            }
            let _ = writeln!(file, "[{}] {} {}: {}{}", now, level, PLUGIN_NAME, msg, txt_fields);
        }
    }
}

fn get_random_u64() -> u64 {
    let mut buf = [0u8; 8];
    if let Ok(mut f) = File::open("/dev/urandom") {
        if f.read_exact(&mut buf).is_ok() {
            return u64::from_ne_bytes(buf);
        }
    }
    // Fallback if /dev/urandom is unavailable
    let now = SystemTime::now()
        .duration_since(SystemTime::UNIX_EPOCH)
        .map(|d| d.as_nanos())
        .unwrap_or(42);
    let pid = std::process::id() as u128;
    ((now ^ (pid << 32)) & 0xFFFF_FFFF_FFFF_FFFF) as u64
}

fn main() {
    let args: Vec<String> = env::args().skip(1).collect();

    if let Some(first) = args.first() {
        match first.as_str() {
            "--ai-docs" => {
                println!("{}", get_ai_docs());
                return;
            }
            "--version" | "-v" => {
                println!("{} {}", PLUGIN_NAME, VERSION);
                return;
            }
            "--help" | "-h" => {
                println!("{} - Rust coin flip plugin for Ash\n\nUsage:\n  {} [--count N] [--format text|json]\n\nFlags:\n  --count N     Number of coins to flip (default: 1)\n  --format FMT  Output format: 'text' (default) or 'json'\n  --ai-docs     Print AI documentation and exit\n  -v, --version Print version and exit\n  -h, --help    Print help and exit", PLUGIN_NAME, PLUGIN_NAME);
                return;
            }
            _ => {}
        }
    }

    log_event("DEBUG", "executing flip_a_coin plugin", &[("EID", "flpC01a")]);

    let mut count: usize = 1;
    let mut format = "json".to_string();

    let mut i = 0;
    while i < args.len() {
        match args[i].as_str() {
            "--count" if i + 1 < args.len() => {
                if let Ok(c) = args[i + 1].parse::<usize>() {
                    count = c.max(1);
                }
                i += 1;
            }
            "--format" if i + 1 < args.len() => {
                format = args[i + 1].clone();
                i += 1;
            }
            _ => {}
        }
        i += 1;
    }

    let mut flips = Vec::with_capacity(count);
    let mut heads = 0;
    let mut tails = 0;

    let mut seed = get_random_u64();
    for _ in 0..count {
        // xorshift64
        seed ^= seed << 13;
        seed ^= seed >> 7;
        seed ^= seed << 17;
        if seed.is_multiple_of(2) {
            flips.push("HEADS");
            heads += 1;
        } else {
            flips.push("TAILS");
            tails += 1;
        }
    }

    if format == "json" {
        let flips_json: Vec<String> = flips.iter().map(|f| format!("\"{}\"", f)).collect();
        let single_result = if count == 1 {
            format!("\"result\": \"{}\",", flips[0])
        } else {
            String::new()
        };
        println!(
            "{{\n  \"status\": \"success\",\n  {}\n  \"flips\": [{}],\n  \"heads_count\": {},\n  \"tails_count\": {}\n}}",
            single_result,
            flips_json.join(", "),
            heads,
            tails
        );
    } else if count == 1 {
        println!("{}", flips[0]);
    } else {
        println!("{}", flips.join(" "));
    }

    log_event("DEBUG", "completed flip_a_coin execution", &[("EID", "flpC02b")]);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_ai_docs_valid_json() {
        let docs = get_ai_docs();
        assert!(docs.contains("\"Capabilities\""));
        assert!(docs.contains("\"Arguments\""));
        assert!(docs.contains("\"Return format\""));
    }

    #[test]
    fn test_randomness_distribution() {
        let mut seed = get_random_u64();
        let trials = 10_000;
        let mut heads = 0;
        let mut tails = 0;

        for _ in 0..trials {
            seed ^= seed << 13;
            seed ^= seed >> 7;
            seed ^= seed << 17;
            if seed % 2 == 0 {
                heads += 1;
            } else {
                tails += 1;
            }
        }

        assert_eq!(heads + tails, trials);
        // Expect roughly 50% (+/- 4% margin of error for 10k flips)
        let ratio = heads as f64 / trials as f64;
        assert!(ratio >= 0.45 && ratio <= 0.55, "Heads ratio {} outside [0.45, 0.55]", ratio);
    }

    #[test]
    fn test_random_seed_generation() {
        let s1 = get_random_u64();
        let s2 = get_random_u64();
        // Two consecutive seeds should not be identical (astronomically unlikely)
        assert_ne!(s1, s2);
    }
}
