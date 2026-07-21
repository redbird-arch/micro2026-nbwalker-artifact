import os
import pandas as pd
from benchmark import get_high_mpki_benchmarks

def harmonic_mean(df: pd.DataFrame) -> float:
    data = df.tolist()
    if not data or any(x <= 0 for x in data):
        return 0.0
    reciprocal_sum = sum(1 / x for x in data)
    return len(data) / reciprocal_sum

def collect_performance_data(
    benchmark_name: str,
    input_dir: str,
) -> float:
    """
    Collects performance data from the specified input directory.

    Args:
        benchmark_name (str): The name of the benchmark.
        input_dir (str): The directory containing performance data files.

    Returns:
        list: A list of dictionaries containing performance data.
    """
    performance_data = 0
    count = 0

    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")

    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")

        return performance_data

    # read the CSV file and collect performance data
    df = pd.read_csv(file_path)

    for _, row in df.iterrows():
        if "MMU" in row.iloc[1] and row.iloc[2] == " req_average_latency":
            performance_data += row.iloc[3]
            count += 1

    if count == 0:
        print(f"No valid performance data found in {file_path}.")

        return 0.0


    return float(performance_data) / float(count)

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
            Opt1["Data"][i] / baseline["Data"][i]
            if Opt1["Data"][i] != 0 and baseline["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    Opt2["Data"] = [
        (
            Opt2["Data"][i] / baseline["Data"][i]
            if Opt2["Data"][i] != 0 and baseline["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    Opt3["Data"] = [
        (
            Opt3["Data"][i] / baseline["Data"][i]
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
            "NBWalker": Opt1["Data"],
            "NBWalker+AMR": Opt2["Data"],
            "Infinite-Walker": Opt3["Data"],
        }
    )

    output_path = os.path.join(output_dir, "figure_14.csv")
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
        Opt1     = append_row(Opt1,     benchmark, os.path.join(os.environ["PROJECT_ROOT"], "results", "NBWalker"))
        Opt2     = append_row(Opt2,     benchmark, os.path.join(os.environ["PROJECT_ROOT"], "results", "NBWalker+AMR"))
        Opt3     = append_row(Opt3,     benchmark, os.path.join(os.environ["PROJECT_ROOT"], "results", "Infinite-Walker"))

    generate_output_csv(
        baseline=baseline.copy(),
        Opt1=Opt1.copy(),
        Opt2=Opt2.copy(),
        Opt3=Opt3.copy(),
    )