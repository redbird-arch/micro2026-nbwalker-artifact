import os
import pandas as pd
from benchmark import get_high_mpki_benchmarks

def harmonic_mean(df: pd.DataFrame) -> float:
    data = df.tolist()
    if not data or any(x <= 0 for x in data):
        return 0.0
    reciprocal_sum = sum(1 / x for x in data)
    return len(data) / reciprocal_sum


def collect_performance_data(benchmark_name: str, input_dir: str) -> float:
    performance_data = 0
    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")
    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")
        return performance_data
    df = pd.read_csv(file_path)
    for _, row in df.iterrows():
        if row.iloc[1] == " driver" and row.iloc[2] == " kernel_time":
            performance_data = row.iloc[3]
            if performance_data == 0:
                print(f"Warning: kernel time for {benchmark_name} is zero.")
                continue
            else:
                break
        if (
            row.iloc[1] == " GPU1.CommandProcessor"
            and row.iloc[2] == " kernel_time (force stop) 0"
        ):
            performance_data = row.iloc[3]
            print(f"Warning: kernel time (force stop) for {benchmark_name} is {performance_data}.")
            break
    return performance_data

def generate_output_csv(
    baseline: pd.DataFrame,
    Opt1: pd.DataFrame,
    Opt2: pd.DataFrame,
    Opt3: pd.DataFrame,
) -> None:
    benchmarks = get_high_mpki_benchmarks()
    
    # Normalize the time
    Opt1["Data"] = [
        (
            baseline["Data"][i] / Opt1["Data"][i]
            if Opt1["Data"][i] != 0 and baseline["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    Opt2["Data"] = [
        (
            baseline["Data"][i] / Opt2["Data"][i]
            if Opt2["Data"][i] != 0 and baseline["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    Opt3["Data"] = [
        (
            baseline["Data"][i] / Opt3["Data"][i]
            if Opt3["Data"][i] != 0 and baseline["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    baseline["Data"] = [1.0 for _ in range(len(benchmarks))]

    # Ave.
    baseline = pd.concat(
        [
            baseline,
            pd.DataFrame(
                {
                    "Benchmark": ["HMean"],
                    "Data": [harmonic_mean(baseline["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    Opt1 = pd.concat(
        [
            Opt1,
            pd.DataFrame(
                {
                    "Benchmark": ["HMean"],
                    "Data": [harmonic_mean(Opt1["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    Opt2 = pd.concat(
        [
            Opt2,
            pd.DataFrame(
                {
                    "Benchmark": ["HMean"],
                    "Data": [harmonic_mean(Opt2["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    Opt3 = pd.concat(
        [
            Opt3,
            pd.DataFrame(
                {
                    "Benchmark": ["HMean"],
                    "Data": [harmonic_mean(Opt3["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )

    output_dir = os.path.join(os.environ["PROJECT_ROOT"], "reproduce")
    os.makedirs(output_dir, exist_ok=True)
    
    # Save the data to one CSV files
    output_df = pd.DataFrame(
        {
            "Benchmark": baseline["Benchmark"],
            "Baseline": baseline["Data"],
            "MPW": Opt1["Data"],
            "SoftWalker": Opt2["Data"],
            "NBWalker+AMR": Opt3["Data"],
        }
    )

    output_path = os.path.join(output_dir, "figure_25.csv")
    output_df.to_csv(output_path, index=False)

    print(f"Normalized performance data saved to: {output_path}")

if __name__ == "__main__":
    baseline = pd.DataFrame(columns=["Benchmark", "Data"])
    Opt1     = pd.DataFrame(columns=["Benchmark", "Data"])
    Opt2     = pd.DataFrame(columns=["Benchmark", "Data"])
    Opt3     = pd.DataFrame(columns=["Benchmark", "Data"])

    for benchmark in get_high_mpki_benchmarks():
        def append_row(df, benchmark, input_dir):
            perf_data = collect_performance_data(benchmark_name=benchmark, input_dir=input_dir)
            return pd.concat(
                [df, pd.DataFrame({"Benchmark": [benchmark], "Data": [perf_data]})],
                ignore_index=True,
            )

        baseline = append_row(baseline, benchmark, os.path.join(os.environ["PROJECT_ROOT"], "results", "Baseline"))
        Opt1     = append_row(Opt1,     benchmark, os.path.join(os.environ["PROJECT_ROOT"], "results", "MPW"))
        Opt2     = append_row(Opt2,     benchmark, os.path.join(os.environ["PROJECT_ROOT"], "results", "SoftWalker"))
        Opt3     = append_row(Opt3,     benchmark, os.path.join(os.environ["PROJECT_ROOT"], "results", "NBWalker+AMR"))

    generate_output_csv(
        baseline=baseline.copy(),
        Opt1=Opt1.copy(),
        Opt2=Opt2.copy(),
        Opt3=Opt3.copy(),
    )